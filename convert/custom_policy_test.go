package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
)

func TestConvertCustomPoliciesToDBLessPlugins(t *testing.T) {
	doc := &aigw.Document{CustomPolicies: []aigw.CustomPolicy{
		{
			ID:      "00000000-0000-0000-0000-000000000001",
			Name:    "streaming-policy",
			Type:    "streaming",
			Schema:  "return { name = 'streaming-policy' }",
			Handler: "return {}",
		},
		{
			Name:    "installed-policy",
			Type:    "installed",
			Schema:  "return { name = 'installed-policy' }",
			Handler: "return {}",
		},
	}}

	c := newConverter(doc, Options{})
	require.NoError(t, c.run())
	require.Len(t, c.out.CustomPlugins, 1)
	require.Equal(t, "streaming-policy", c.out.CustomPlugins[0].Name)

	out := c.out.ToDBLess()
	require.Len(t, out.CustomPlugins, 1)
	plugin := out.CustomPlugins[0]
	require.Equal(t, "00000000-0000-0000-0000-000000000001", plugin.ID)
	require.Equal(t, "streaming-policy", plugin.Name)
	require.Equal(t, "return { name = 'streaming-policy' }", plugin.Schema)
	require.Equal(t, "return {}", plugin.Handler)
}

func TestConvertCustomPoliciesSkipsPoliciesWithoutStreamingHandler(t *testing.T) {
	for _, policy := range []aigw.CustomPolicy{
		{
			Name:   "streaming-policy",
			Type:   "streaming",
			Schema: "return { name = 'streaming-policy' }",
		},
		{
			Name:    "installed-policy",
			Type:    "installed",
			Schema:  "return { name = 'installed-policy' }",
			Handler: "return {}",
		},
	} {
		c := newConverter(&aigw.Document{CustomPolicies: []aigw.CustomPolicy{policy}}, Options{})

		require.NoError(t, c.run())
		require.Empty(t, c.out.ToDBLess().CustomPlugins)
	}
}
