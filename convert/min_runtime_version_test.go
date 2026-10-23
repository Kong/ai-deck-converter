package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
)

func TestConvertRejectsInvalidMinRuntimeVersion(t *testing.T) {
	src := []byte("models: []\n")
	for version, want := range map[string]string{
		"":       "MinRuntimeVersion is required",
		"latest": "invalid MinRuntimeVersion",
		"2.1.0":  "invalid MinRuntimeVersion",
		"1.9":    "older than the oldest supported release",
	} {
		result, err := Convert(src, Options{MinRuntimeVersion: version})
		require.ErrorContains(t, err, want, "%q", version)
		require.Nil(t, result, "%q", version)
	}
}

func TestConvertRecordsMinRuntimeVersion(t *testing.T) {
	result, err := Convert([]byte("models: []\n"), Options{MinRuntimeVersion: "2.1"})
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Equal(t, "2.1", result.Metadata.MinRuntimeVersion)
}

func TestConvertFallsBackFromUnknownMinRuntimeVersion(t *testing.T) {
	result, err := Convert([]byte("models: []\n"), Options{MinRuntimeVersion: "99.0"})
	require.NoError(t, err)
	require.Equal(t, aimap.LatestVersion.String(), result.Metadata.MinRuntimeVersion)
	require.Len(t, result.Issues, 1)
	issue := result.Issues[0]
	require.Equal(t, Warning, issue.Severity)
	require.Equal(t, ConfigFieldOverridden, issue.Code)
	require.Equal(t, ConversionIssueSource{Kind: kindConversionOption, Name: "min_runtime_version"}, issue.Source)
}

// TestCheckModelCapabilitiesGatesOnlyExplicitCapabilities: "*" leaves out a
// gated capability with no issue. A listed "skills" below its gate fails.
func TestCheckModelCapabilitiesGatesOnlyExplicitCapabilities(t *testing.T) {
	below := aimap.Since(aimap.FeatureSkillsCapability)
	below.Minor--
	c := newConverter(nil, Options{})
	c.minRuntimeVersion = below

	m := &aigw.Model{Name: "raw-gpt", Capabilities: []string{"skills"}}
	src := issueSource(kindModel, m.Name)

	require.NoError(t, c.checkModelCapabilities(src, m, modelLists{formats: []string{"openai"}, capsWildcard: true}),
		"a wildcard must never gate the model")
	require.Error(t, c.checkModelCapabilities(src, m, modelLists{
		formats: []string{"openai"}, capabilities: []string{"skills"},
	}), "a listed capability below its gate must still fail")
}

// TestDisabledAPIModelIsNeverGated: a disabled, non-passthrough type:api
// model shares its route-scoped plugins with other api models, so convertModel
// excludes it before it emits anything (see the comment at its early return).
// A gate below it must never fire, since the model was never going to convert
// either way: a gate failure here would report an issue for output that does
// not exist.
func TestDisabledAPIModelIsNeverGated(t *testing.T) {
	src := []byte(`
models:
  - name: disabled-skills-api
    type: api
    enabled: false
    capabilities: [skills]
    formats: [{type: openai}]
    targets:
      - name: gpt-5.6
        provider: openai-prod
        config: {type: openai}
model_providers:
  - name: openai-prod
    type: openai
    config:
      auth:
        type: basic
        headers:
          - name: Authorization
            value: "{vault://ai/openai-token}"
`)
	result, err := Convert(src, Options{MinRuntimeVersion: "2.1"})
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Output.Plugins)
}
