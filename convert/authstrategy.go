package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// anonymousConsumerName is the username/custom_id of the synthesized Consumer
// that auth-strategy authentication plugins fall back to when a request
// isn't authenticated, so it can be rejected by a request-termination plugin
// instead of silently reaching the upstream.
const (
	anonymousConsumerName    = "anonymous"
	unauthorizedStatusCode   = 401
	unauthorizedErrorMessage = "Unauthorized"
)

// scopedAuthStrategyPlugins builds one authentication plugin per auth strategy
// reference, each configured to fall back to the anonymous consumer.
func (c *Converter) scopedAuthStrategyPlugins(refs []string) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		idp := c.authStrategies[ref]
		if idp == nil {
			if err := c.warn("unknown auth strategy reference %q", ref); err != nil {
				return nil, err
			}
			continue
		}
		plugins = append(plugins, authStrategyPlugin(idp))
	}
	return plugins, nil
}

// authStrategyPlugin builds a Kong authentication plugin from an AI
// Gateway auth strategy, with config.anonymous set so failed
// authentication falls back to the anonymous consumer instead of erroring.
func authStrategyPlugin(idp *aigw.AuthStrategy) kong.Plugin {
	cfg := make(map[string]any, len(idp.Config)+1)
	for k, v := range idp.Config {
		cfg[k] = v
	}
	cfg["anonymous"] = anonymousConsumerName
	if idp.Type == "key-auth" {
		forceKeyAuthIdentityRealms(cfg)
	}
	return kong.Plugin{
		Name:   idp.Type,
		Config: cfg,
		Source: source("identity_provider", idp.Name, "config"),
	}
}

// forceKeyAuthIdentityRealms enforces the key-auth schema constraint that when
// principal hydration is enabled (config.principals.enabled = true),
// config.identity_realms must be present as an empty array rather than unset,
// as identity_realms is set by the DP to the default realm.
// See https://github.com/Kong/kong-ee/blob/master/kong/plugins/key-auth/schema.lua#L63
func forceKeyAuthIdentityRealms(cfg map[string]any) {
	principals, ok := cfg["principals"].(map[string]any)
	if !ok {
		return
	}
	if enabled, _ := principals["enabled"].(bool); !enabled {
		return
	}
	cfg["identity_realms"] = []any{}
}

// ensureAnonymousConsumer appends the anonymous Consumer (with a
// request-termination plugin so unauthenticated requests get a 401) if one
// isn't already present in the output document.
func (c *Converter) ensureAnonymousConsumer() {
	enabled := true
	requestTerminationPlugin := kong.Plugin{
		Name:    "request-termination",
		Enabled: &enabled,
		Config: map[string]any{
			"status_code": unauthorizedStatusCode,
			"message":     unauthorizedErrorMessage,
		},
	}

	// Overwrite for both 'username: anonymous' and 'custom_id: anonymous' because
	// our auth plugins look for both at random unpredictable loop iterations
	providedConfigContainsAnonConsumer := false
	for i := range c.out.Consumers {
		if c.out.Consumers[i].Username == anonymousConsumerName || c.out.Consumers[i].CustomID == anonymousConsumerName {
			providedConfigContainsAnonConsumer = true
			requestTerminationPluginOverwritten := false
			for j, p := range c.out.Consumers[i].Plugins {
				if p.Name == "request-termination" {
					c.out.Consumers[i].Plugins[j] = requestTerminationPlugin
					requestTerminationPluginOverwritten = true
					break
				}
			}
			if !requestTerminationPluginOverwritten {
				c.out.Consumers[i].Plugins = append(c.out.Consumers[i].Plugins, requestTerminationPlugin)
			}
		}
	}
	if providedConfigContainsAnonConsumer {
		return
	}

	c.out.Consumers = append(c.out.Consumers, kong.Consumer{
		Username: anonymousConsumerName,
		CustomID: anonymousConsumerName,
		Plugins:  []kong.Plugin{requestTerminationPlugin},
	})
}
