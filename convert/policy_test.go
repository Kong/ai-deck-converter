package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

// TestSanitizerAnonymizeCollapseFollowsMinRuntimeVersion pins the 2.3 gate.
// The 136_ golden case covers the output below the gate.
func TestSanitizerAnonymizeCollapseFollowsMinRuntimeVersion(t *testing.T) {
	src := []byte(`
policies:
  - name: san
    type: ai-sanitizer
    global: true
    config: {anonymize: [all, credentials]}
`)
	for version, want := range map[string][]any{
		"2.0": {"all_and_credentials"},
		"2.2": {"all_and_credentials"},
		"2.3": {"all", "credentials"},
		"2.4": {"all", "credentials"},
	} {
		result, err := Convert(src, Options{MinRuntimeVersion: version})
		require.NoError(t, err, version)
		require.Empty(t, result.Issues, version)
		require.Len(t, result.Output.Plugins, 1, version)
		require.Equal(t, want, result.Output.Plugins[0].Config["anonymize"], version)
	}
}

func geminiBlock(t *testing.T, config map[string]any, block string) map[string]any {
	t.Helper()
	b, ok := config[block].(map[string]any)
	require.True(t, ok, block)
	model, ok := b["model"].(map[string]any)
	require.True(t, ok, block+".model")
	options, ok := model["options"].(map[string]any)
	require.True(t, ok, block+".model.options")
	gemini, ok := options["gemini"].(map[string]any)
	require.True(t, ok, block+".model.options.gemini")
	return gemini
}

func pluginConfig(t *testing.T, plugins []kong.Plugin, name string) map[string]any {
	t.Helper()
	for _, p := range plugins {
		if p.Name == name {
			return p.Config
		}
	}
	require.Failf(t, "plugin not found", "%s", name)
	return nil
}

const geminiPolicySrc = `
policies:
  - name: judge
    type: ai-llm-as-judge
    global: true
    config:
      llm:
        model:
          provider: gemini
          name: gemini-2.5-pro
          options:
            gemini: {project_id: my-project, location_id: us}
  - name: cache
    type: ai-semantic-cache
    global: true
    config:
      embeddings:
        model:
          provider: gemini
          name: text-embedding-004
          options:
            gemini: {project_id: my-project, location_id: eu, api_endpoint: ""}
`

func TestPolicyGeminiEndpointFillFollowsMinRuntimeVersion(t *testing.T) {
	for _, tt := range []struct {
		version      string
		judge, cache any
	}{
		{"2.3", "aiplatform.us.rep.googleapis.com", "aiplatform.eu.rep.googleapis.com"},
		{"2.4", nil, ""},
	} {
		t.Run(tt.version, func(t *testing.T) {
			result, err := Convert([]byte(geminiPolicySrc), Options{MinRuntimeVersion: tt.version})
			require.NoError(t, err)
			require.Empty(t, result.Issues)
			judge := geminiBlock(t, pluginConfig(t, result.Output.Plugins, "ai-llm-as-judge"), "llm")
			cache := geminiBlock(t, pluginConfig(t, result.Output.Plugins, "ai-semantic-cache"), "embeddings")
			require.Equal(t, tt.judge, judge["api_endpoint"])
			require.Equal(t, tt.cache, cache["api_endpoint"])
		})
	}
}

func TestPolicyGeminiEndpointFillKeepsSource(t *testing.T) {
	doc, err := aigw.Parse([]byte(geminiPolicySrc))
	require.NoError(t, err)
	c := newConverter(doc, Options{MinRuntimeVersion: "2.3"})
	c.minRuntimeVersion = aimap.Version{Major: 2, Minor: 3}
	_, ok := c.policyPlugin(&doc.Policies[0], nil, false)
	require.True(t, ok)
	require.Nil(t, geminiBlock(t, doc.Policies[0].Config, "llm")["api_endpoint"])
}

func TestPolicyGeminiEndpointInvalidLocation(t *testing.T) {
	src := []byte(`
policies:
  - name: judge
    type: ai-llm-as-judge
    global: true
    config:
      llm:
        model:
          provider: gemini
          name: gemini-2.5-pro
          options:
            gemini: {project_id: my-project, location_id: evil.example.com/x}
`)
	result, err := Convert(src, Options{MinRuntimeVersion: "2.3"})
	require.NoError(t, err)
	require.Empty(t, result.Output.Plugins)
	requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindPolicy, "judge",
		"config.llm.model.options.gemini.location_id")
}
