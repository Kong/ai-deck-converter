package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const intelligentProviders = `
model_providers:
  - name: jev-typesafe
    type: typesafe
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "{vault://env/typesafe-token}"}]
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
        classifier: {model: classifier}
    targets:
      - {name: gpt-5-mini, provider: openai}
      - {name: gpt-5, provider: openai}
`

func TestIntelligentClassifierWithoutTarget(t *testing.T) {
	src := intelligentProviders + `
models:` + intelligentModel + `
  - name: classifier
    capabilities: [decisions]
    formats: [{type: typesafe}]
    config:
      route: {paths: [/classifier]}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "auto",
		"config.balancer.classifier.model")
	require.Contains(t, issue.Reason, "has 0 targets")
}

func TestIntelligentClassifierTokenVault(t *testing.T) {
	src := providerTokenVaultDatastores + intelligentProviders + `
  - name: jev-vault
    type: typesafe
    datastores: [{name: tv}]
    token_vault: {provider: typesafe}
models:` + intelligentModel + `
  - name: classifier
    capabilities: [decisions]
    formats: [{type: typesafe}]
    config:
      route: {paths: [/classifier]}
    targets:
      - {name: jev-latest, provider: jev-vault}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "auto",
		"config.balancer.classifier.model")
	require.Contains(t, issue.Reason, "uses a token vault")
}

// A failed model starts a new pass. The intelligent model converts in each
// pass. Its lowering must not change the source document.
func TestIntelligentBalancerSurvivesNewPass(t *testing.T) {
	src := intelligentProviders + `
models:` + intelligentModel + `
  - name: classifier
    capabilities: [decisions]
    formats: [{type: typesafe}]
    config:
      route: {paths: [/classifier]}
    targets:
      - {name: jev-latest, provider: jev-typesafe}
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
	requireIssue(t, result.Issues, Error, UndefinedReference, kindModel, "broken",
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
			"type":     "model",
			"provider": "typesafe",
			"name":     "jev-latest",
			"auth": map[string]any{
				"header_name":  "Authorization",
				"header_value": "{vault://env/typesafe-token}",
			},
		},
	}, balancer)
}

// The classifier follows the decisions capability, not the provider type. A
// target of any provider type with upstream_url gives a custom classifier.
func TestIntelligentClassifierAnyProviderType(t *testing.T) {
	src := intelligentProviders + `
models:` + intelligentModel + `
  - name: classifier
    capabilities: [decisions]
    formats: [{type: typesafe}]
    config:
      route: {paths: [/classifier]}
    targets:
      - name: decider
        provider: openai
        config: {upstream_url: "https://decide.example.com/api/decide"}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	for _, issue := range result.Issues {
		require.Equal(t, "classifier", issue.Source.Name, "issue: %v", issue)
	}
	var classifier map[string]any
	for _, p := range result.Output.Plugins {
		if p.Name == "ai-proxy-advanced" && p.Model != nil && string(*p.Model) == "auto" {
			balancer, _ := p.Config["balancer"].(map[string]any)
			classifier, _ = balancer["classifier"].(map[string]any)
		}
	}
	require.Equal(t, map[string]any{
		"type":   "custom",
		"url":    "https://decide.example.com",
		"format": "/api/decide",
		"name":   "decider",
		"auth": map[string]any{
			"header_name":  "Authorization",
			"header_value": "{vault://env/openai-key}",
		},
	}, classifier)
}
