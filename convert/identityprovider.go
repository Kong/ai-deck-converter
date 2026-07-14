package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// anonymousConsumerName is the username/custom_id of the synthesized Consumer
// that identity-provider authentication plugins fall back to when a request
// isn't authenticated, so it can be rejected by a request-termination plugin
// instead of silently reaching the upstream.
const (
	anonymousConsumerName    = "anonymous"
	unauthorizedStatusCode   = 401
	unauthorizedErrorMessage = "Unauthorized"
)

// scopedIdentityProviderPlugins builds one authentication plugin per identity
// provider reference, each configured to fall back to the anonymous consumer.
func (c *Converter) scopedIdentityProviderPlugins(refs []string) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		idp := c.identityProviders[ref]
		if idp == nil {
			if err := c.warn("unknown identity provider reference %q", ref); err != nil {
				return nil, err
			}
			continue
		}
		plugins = append(plugins, identityProviderPlugin(idp))
	}
	return plugins, nil
}

// identityProviderPlugin builds a Kong authentication plugin from an AI
// Gateway identity provider, with config.anonymous set so failed
// authentication falls back to the anonymous consumer instead of erroring.
func identityProviderPlugin(idp *aigw.IdentityProvider) kong.Plugin {
	cfg := make(map[string]any, len(idp.Config)+1)
	for k, v := range idp.Config {
		cfg[k] = v
	}
	cfg["anonymous"] = anonymousConsumerName
	return kong.Plugin{Name: idp.Type, Config: cfg}
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
