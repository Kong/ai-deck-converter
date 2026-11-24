package revert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionAffinityFromConfig(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cfg  map[string]any
		want any
	}{
		"absent": {cfg: nil, want: nil},
		"local": {
			cfg:  map[string]any{"enabled": true, "ttl": 60, "strategy": "local"},
			want: map[string]any{"enabled": true, "ttl": 60, "storage": map[string]any{"type": "local"}},
		},
		"redis inline connection": {
			cfg: map[string]any{
				"enabled": true, "ttl": 21600, "strategy": "redis",
				"redis": map[string]any{"host": "inline.internal", "port": 6390},
			},
			want: map[string]any{
				"enabled": true, "ttl": 21600,
				"storage": map[string]any{"type": "redis", "host": "inline.internal", "port": 6390},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, sessionAffinityFromConfig(tc.cfg))
		})
	}
}

// Two plugins that differ only in conversation_state belong to different models.
// The grouping compares modelProxyConfig, so it must keep the block.
func TestModelProxyConfigKeepsConversationState(t *testing.T) {
	t.Parallel()

	local := map[string]any{"conversation_state": map[string]any{"strategy": "local"}}
	redis := map[string]any{"conversation_state": map[string]any{"strategy": "redis"}}

	require.NotEqual(t, modelProxyConfig(local), modelProxyConfig(redis))
}
