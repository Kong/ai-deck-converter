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

// TestCheckModelCapabilitiesIgnoresThePassthroughSentinel locks in the fix
// for a passthrough model's capability gate: checkModelCapabilities must see
// the post-override caps ([""]), set by convertModel before it calls this
// function, never the model's declared (and otherwise irrelevant) ones. A
// regression that again passed the pre-override caps would make this test
// fail, because "skills" needs a newer release than "" ever requires.
func TestCheckModelCapabilitiesIgnoresThePassthroughSentinel(t *testing.T) {
	below := aimap.Since(aimap.FeatureSkillsCapability)
	below.Minor--
	c := newConverter(nil, Options{})
	c.minRuntimeVersion = below

	m := &aigw.Model{Name: "raw-gpt", Capabilities: []string{"skills"}}
	src := issueSource(kindModel, m.Name)

	require.NoError(t, c.checkModelCapabilities(src, m, []string{""}),
		"the passthrough sentinel must never gate the model")
	require.Error(t, c.checkModelCapabilities(src, m, []string{"skills"}),
		"a real declared capability below its gate must still fail")
}
