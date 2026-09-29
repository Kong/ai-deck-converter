package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

func TestRevertCustomPolicies(t *testing.T) {
	doc := &kong.Document{CustomPlugins: []kong.CustomPlugin{{
		ID:      "00000000-0000-0000-0000-000000000001",
		Name:    "streaming-policy",
		Schema:  "return { name = 'streaming-policy' }",
		Handler: "return {}",
	}}}

	out, warnings, err := RevertDocument(doc, Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, out.CustomPolicies, 1)
	policy := out.CustomPolicies[0]
	require.Equal(t, "00000000-0000-0000-0000-000000000001", policy.ID)
	require.Equal(t, "streaming-policy", policy.Name)
	require.Equal(t, "streaming", policy.Type)
	require.Equal(t, "return { name = 'streaming-policy' }", policy.Schema)
	require.Equal(t, "return {}", policy.Handler)
}

func TestRevertCustomPoliciesWithoutHandler(t *testing.T) {
	out, warnings, err := RevertDocument(&kong.Document{CustomPlugins: []kong.CustomPlugin{{
		Name:   "installed-policy",
		Schema: "return { name = 'installed-policy' }",
	}}}, Options{})
	require.NoError(t, err)
	require.Empty(t, out.CustomPolicies)
	require.Equal(t, []string{`custom plugin "installed-policy" has no handler`}, warnings)
}
