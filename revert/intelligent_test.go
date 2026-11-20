package revert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var handwrittenIntelligent = filepath.Join("testdata", "76_intelligent_balancer_handwritten", "input.yaml")

const oldShapeClassifier = `
model "old-shape": classifier type "model" is not supported. ` +
	`The AI Gateway API has only type "custom". config.balancer.classifier is left out.`

func TestIntelligentBalancerWarnings(t *testing.T) {
	in, err := os.ReadFile(handwrittenIntelligent)
	require.NoError(t, err)
	_, warnings, err := Revert(in, Options{})
	require.NoError(t, err)
	require.Equal(t, []string{
		`model "near": cost_quality_tradeoff 0.4 is not a preset value. ` +
			`It becomes the nearest preset "balanced", with value 0.5.`,
		`model "margin": margin 12 is not a preset value. It becomes the nearest preset "cost", with margin 15.`,
		`model "margin": cost_quality_tradeoff 0.5 becomes 0.25, the value of preset "cost".`,
		oldShapeClassifier[1:],
	}, warnings)
}

func TestIntelligentBalancerStrict(t *testing.T) {
	in, err := os.ReadFile(handwrittenIntelligent)
	require.NoError(t, err)
	_, _, err = Revert(in, Options{Strict: true})
	require.ErrorContains(t, err, "cost_quality_tradeoff 0.4 is not a preset value")
}

// A record with type model is the old data plane shape. Revert does not guess
// a URL for it.
func TestIntelligentOldClassifierStrict(t *testing.T) {
	in := []byte(`_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - {name: openai-chat, paths: [/old-shape/chat/completions], methods: [POST, OPTIONS], strip_path: false}
plugins:
  - name: ai-proxy-advanced
    route: openai-chat
    model: old-shape
    config:
      balancer:
        algorithm: intelligent
        cost_quality_tradeoff: 0.5
        classifier: {type: model, provider: typesafe, name: jev-latest}
      genai_category: text/generation
      llm_format: openai
      targets:
        - model: {model_alias: old-shape, name: gpt-5-mini, provider: openai}
          route_type: llm/v1/chat
ai_models:
  - name: old-shape
`)
	_, _, err := Revert(in, Options{Strict: true})
	require.EqualError(t, err, oldShapeClassifier[1:])
}
