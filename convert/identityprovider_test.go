package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

func TestEnsureAnonymousConsumerCreatesWhenMissing(t *testing.T) {
	c := newConverter(nil, Options{})

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 1, "expected anonymous consumer to be created")
	anon := c.out.Consumers[0]
	require.Equal(t, anonymousConsumerName, anon.Username)
	require.Equal(t, anonymousConsumerName, anon.CustomID)
	require.Len(t, anon.Plugins, 1, "expected request-termination plugin to be attached")
	require.Equal(t, "request-termination", anon.Plugins[0].Name)
	require.Equal(t, map[string]any{
		"status_code": unauthorizedStatusCode,
		"message":     unauthorizedErrorMessage,
	}, anon.Plugins[0].Config)
}

func TestEnsureAnonymousConsumerAttachesPluginWhenMissing(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{{
		Username: anonymousConsumerName,
		CustomID: anonymousConsumerName,
		Plugins: []kong.Plugin{{
			Name:   "rate-limiting",
			Config: map[string]any{"minute": 5},
		}},
	}}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 1, "should not duplicate the anonymous consumer")
	anon := c.out.Consumers[0]
	require.Len(t, anon.Plugins, 2, "expected request-termination plugin to be appended")
	require.Equal(t, "rate-limiting", anon.Plugins[0].Name, "existing plugin should be preserved")
	require.Equal(t, "request-termination", anon.Plugins[1].Name)
	require.Equal(t, map[string]any{
		"status_code": unauthorizedStatusCode,
		"message":     unauthorizedErrorMessage,
	}, anon.Plugins[1].Config)
}

func TestEnsureAnonymousConsumerHandlesSeparateUsernameAndCustomIDConsumers(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{
		{Username: anonymousConsumerName},
		{CustomID: anonymousConsumerName},
	}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 2, "should not create a new consumer when matches already exist")

	byUsername := c.out.Consumers[0]
	require.Len(t, byUsername.Plugins, 1, "consumer matched by username should get the plugin")
	require.Equal(t, "request-termination", byUsername.Plugins[0].Name)

	byCustomID := c.out.Consumers[1]
	require.Len(t, byCustomID.Plugins, 1, "consumer matched by custom_id should also get the plugin")
	require.Equal(t, "request-termination", byCustomID.Plugins[0].Name)
}

func TestEnsureAnonymousConsumerLeavesUnrelatedConsumersUntouched(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{
		{
			Username: "someone-else",
			CustomID: "someone-else",
			Plugins: []kong.Plugin{{
				Name:   "rate-limiting",
				Config: map[string]any{"minute": 5},
			}},
		},
	}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 2, "should append a new anonymous consumer alongside the unrelated one")

	unrelated := c.out.Consumers[0]
	require.Equal(t, "someone-else", unrelated.Username)
	require.Len(t, unrelated.Plugins, 1, "unrelated consumer's plugins should be untouched")
	require.Equal(t, "rate-limiting", unrelated.Plugins[0].Name)

	anon := c.out.Consumers[1]
	require.Equal(t, anonymousConsumerName, anon.Username)
	require.Equal(t, anonymousConsumerName, anon.CustomID)
	require.Len(t, anon.Plugins, 1, "expected request-termination plugin to be attached")
	require.Equal(t, "request-termination", anon.Plugins[0].Name)
}

func TestEnsureAnonymousConsumerOverwritesExistingPlugin(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{{
		Username: anonymousConsumerName,
		CustomID: anonymousConsumerName,
		Plugins: []kong.Plugin{{
			Name:   "request-termination",
			Config: map[string]any{"status_code": 403, "message": "custom"},
		}},
	}}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 1, "should not duplicate the anonymous consumer")
	anon := c.out.Consumers[0]
	require.Len(t, anon.Plugins, 1, "should not add a second request-termination plugin")
	require.Equal(t, map[string]any{
		"status_code": unauthorizedStatusCode,
		"message":     unauthorizedErrorMessage,
	}, anon.Plugins[0].Config, "existing plugin config should be forcefully overwritten")
	require.NotNil(t, anon.Plugins[0].Enabled)
	require.True(t, *anon.Plugins[0].Enabled, "overwritten plugin should always be enabled")
}
