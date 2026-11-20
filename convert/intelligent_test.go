package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const intelligentProviders = `
model_providers:
  - name: openai
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "{vault://env/openai-key}"}]
`

const intelligentModel = `
  - name: auto
    capabilities: [generate]
    config:
      route: {paths: [/auto]}
      balancer:
        algorithm: intelligent
        cost_quality_tradeoff: cost
        classifier:
          type: custom
          url: https://api.typesafe.ai
          name: jev-latest
          context_window: 32768
          auth:
            type: basic
            headers: [{name: Authorization, value: "{vault://env/typesafe-token}"}]
    targets:
      - {name: gpt-5-mini, provider: openai}
      - {name: gpt-5, provider: openai}
`

// A failed model starts a new pass. The intelligent model converts in each
// pass. Its lowering must not change the source document.
func TestIntelligentBalancerSurvivesNewPass(t *testing.T) {
	src := intelligentProviders + `
models:` + intelligentModel + `
  - name: broken
    capabilities: [generate]
    config:
      route: {paths: [/broken]}
      balancer: {algorithm: intelligent, classifier: {model: nope}}
    targets:
      - {name: gpt-5, provider: openai}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1, "issues: %v", result.Issues)
	requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "broken",
		"config.balancer.classifier.model")

	var balancer map[string]any
	for _, p := range result.Output.Plugins {
		if p.Name == "ai-proxy-advanced" && p.Model != nil && string(*p.Model) == "auto" {
			balancer, _ = p.Config["balancer"].(map[string]any)
		}
	}
	require.Equal(t, map[string]any{
		"algorithm":             "intelligent",
		"cost_quality_tradeoff": 0.25,
		"strategy":              "weighted",
		"classifier": map[string]any{
			"type":           "custom",
			"url":            "https://api.typesafe.ai",
			"format":         "/v1/systemone",
			"name":           "jev-latest",
			"context_window": 32768,
			"auth": map[string]any{
				"header_name":  "Authorization",
				"header_value": "{vault://env/typesafe-token}",
			},
		},
	}, balancer)
}
