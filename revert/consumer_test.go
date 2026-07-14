package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

func syntheticRequestTerminationPlugin() kong.Plugin {
	return kong.Plugin{
		Name: "request-termination",
		Config: map[string]any{
			"status_code": unauthorizedStatusCode,
			"message":     "Unauthorized",
		},
	}
}

func TestIsSynthesizedAnonymousConsumerMatchesUsernameOrCustomIDAlone(t *testing.T) {
	cases := []struct {
		name string
		c    kong.Consumer
	}{
		{
			name: "both username and custom_id set",
			c: kong.Consumer{
				Username: anonymousConsumerName,
				CustomID: anonymousConsumerName,
				Plugins:  []kong.Plugin{syntheticRequestTerminationPlugin()},
			},
		},
		{
			name: "username only",
			c: kong.Consumer{
				Username: anonymousConsumerName,
				Plugins:  []kong.Plugin{syntheticRequestTerminationPlugin()},
			},
		},
		{
			name: "custom_id only",
			c: kong.Consumer{
				CustomID: anonymousConsumerName,
				Plugins:  []kong.Plugin{syntheticRequestTerminationPlugin()},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, isSynthesizedAnonymousConsumer(&tc.c))
		})
	}
}

func TestIsSynthesizedAnonymousConsumerRejectsOtherShapes(t *testing.T) {
	cases := []struct {
		name string
		c    kong.Consumer
	}{
		{
			name: "not named anonymous",
			c: kong.Consumer{
				Username: "someone-else",
				CustomID: "someone-else",
				Plugins:  []kong.Plugin{syntheticRequestTerminationPlugin()},
			},
		},
		{
			name: "has extra plugin",
			c: kong.Consumer{
				Username: anonymousConsumerName,
				CustomID: anonymousConsumerName,
				Plugins: []kong.Plugin{
					syntheticRequestTerminationPlugin(),
					{Name: "rate-limiting", Config: map[string]any{"minute": 5}},
				},
			},
		},
		{
			name: "has credentials",
			c: kong.Consumer{
				Username:           anonymousConsumerName,
				CustomID:           anonymousConsumerName,
				Plugins:            []kong.Plugin{syntheticRequestTerminationPlugin()},
				KeyAuthCredentials: []kong.KeyAuthCredential{{Key: "abc"}},
			},
		},
		{
			name: "custom plugin config",
			c: kong.Consumer{
				Username: anonymousConsumerName,
				CustomID: anonymousConsumerName,
				Plugins: []kong.Plugin{{
					Name:   "request-termination",
					Config: map[string]any{"status_code": 403, "message": "Unauthorized"},
				}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, isSynthesizedAnonymousConsumer(&tc.c))
		})
	}
}
