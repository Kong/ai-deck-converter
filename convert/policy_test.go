package convert

import (
	"testing"

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
