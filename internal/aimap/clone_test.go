package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The clone must run after ai-proxy-advanced (which owns the response body) and
// before the AI plugins that consume it for logging.
func TestResponseTransformerClonePriority(t *testing.T) {
	require.Less(t, ResponseTransformerClonePriority, PluginPriority["ai-proxy-advanced"])
	require.Less(t, ResponseTransformerClonePriority, PluginPriority["response-transformer"])
	require.Less(t, PluginPriority["ai-semantic-cache"], ResponseTransformerClonePriority)

	for name, priority := range PluginPriority {
		require.NotEqual(t, ResponseTransformerClonePriority, priority,
			"the clone priority collides with plugin %q", name)
	}
}

// Each response transformer has exactly one clone name, and the name maps back
// to its base plugin.
func TestPostAICloneName(t *testing.T) {
	for base, clone := range map[string]string{
		"response-transformer":          ResponseTransformerCloneName,
		"response-transformer-advanced": ResponseTransformerAdvancedCloneName,
	} {
		name, ok := PostAICloneName(base)
		require.True(t, ok, base)
		require.Equal(t, clone, name, base)

		ref, ok := CloneRef(clone)
		require.True(t, ok, clone)
		require.Equal(t, base, ref, clone)
	}

	_, ok := PostAICloneName("ai-proxy-advanced")
	require.False(t, ok)
	_, ok = CloneRef("response-transformer")
	require.False(t, ok)
}
