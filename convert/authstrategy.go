package convert

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

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
// reference of the entity src, each configured to fall back to the anonymous
// consumer. An unknown reference fails the entity, so it is never left
// unprotected.
func (c *Converter) scopedAuthStrategyPlugins(src ConversionIssueSource, refs []string) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for i, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		idp := c.authStrategies[ref]
		if idp == nil {
			return nil, c.fail(src.at(fmt.Sprintf("access.auth_strategies[%d]", i)), UndefinedReference,
				"unknown auth strategy reference %q", ref)
		}
		c.warnAuthStrategyOverrides(idp)
		plugins = append(plugins, authStrategyPlugin(idp))
	}
	return plugins, nil
}

// warnAuthStrategyOverrides reports the auth strategy config values that
// authStrategyPlugin replaces.
func (c *Converter) warnAuthStrategyOverrides(idp *aigw.AuthStrategy) {
	src := issueSource(kindAuthStrategy, idp.Name)
	if v := idp.Config["anonymous"]; !isEmptyConfigValue(v) && v != "" && v != anonymousConsumerName {
		c.warn(src.at("config.anonymous"), ConfigFieldOverridden,
			"auth strategy %q sets config.anonymous to %v. "+
				"This will be ignored, and anonymous will be overridden to %q "+
				"to reject unauthenticated requests with a request-termination plugin.",
			idp.Name, v, anonymousConsumerName)
	}
	if idp.Type == "key-auth" && keyAuthPrincipalsEnabled(idp.Config) {
		if v, ok := idp.Config["identity_realms"]; ok && !isEmptyConfigValue(v) {
			c.warn(src.at("config.identity_realms"), ConfigFieldOverridden,
				"auth strategy %q enables config.principals "+
					"and sets config.identity_realms. "+
					"This will be ignored, and identity_realms will be overridden to [] "+
					"because the key-auth schema requires it when principals is enabled.",
				idp.Name)
		}
	}
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

// withoutAnonymousFallback returns a copy of p without config.anonymous.
// request-termination does not run on ws and wss routes.
// An anonymous fallback there lets unauthenticated clients through.
func withoutAnonymousFallback(p kong.Plugin) kong.Plugin {
	cfg := make(map[string]any, len(p.Config))
	maps.Copy(cfg, p.Config)
	delete(cfg, "anonymous")
	p.Config = cfg
	return p
}

// forceKeyAuthIdentityRealms enforces the key-auth schema constraint that when
// principal hydration is enabled (config.principals.enabled = true),
// config.identity_realms must be present as an empty array rather than unset,
// as identity_realms is set by the DP to the default realm.
// See https://github.com/Kong/kong-ee/blob/master/kong/plugins/key-auth/schema.lua#L63
func forceKeyAuthIdentityRealms(cfg map[string]any) {
	if keyAuthPrincipalsEnabled(cfg) {
		cfg["identity_realms"] = []any{}
	}
}

func keyAuthPrincipalsEnabled(cfg map[string]any) bool {
	principals, ok := cfg["principals"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := principals["enabled"].(bool)
	return enabled
}

// ensureAnonymousConsumer appends the anonymous Consumer (with a
// request-termination plugin so unauthenticated requests get a 401) if one
// isn't already present in the output document.
//
// It never changes a consumer in place. convertEntity and convertModels
// restore c.out from a shallow copy, so an in-place change would survive a
// rollback.
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
	var consumers []kong.Consumer
	for i := range c.out.Consumers {
		if c.out.Consumers[i].Username != anonymousConsumerName && c.out.Consumers[i].CustomID != anonymousConsumerName {
			continue
		}
		providedConfigContainsAnonConsumer = true
		plugins, changed := withPlugin(c.out.Consumers[i].Plugins, requestTerminationPlugin)
		if !changed {
			continue
		}
		if consumers == nil {
			consumers = slices.Clone(c.out.Consumers)
		}
		consumers[i].Plugins = plugins
	}
	if consumers != nil {
		c.out.Consumers = consumers
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

// withPlugin returns a copy of plugins in which p replaces the first plugin of
// the same name, or p is appended. It reports false when plugins already has
// p, and then returns plugins unchanged.
func withPlugin(plugins []kong.Plugin, p kong.Plugin) ([]kong.Plugin, bool) {
	i := slices.IndexFunc(plugins, func(q kong.Plugin) bool { return q.Name == p.Name })
	if i >= 0 && reflect.DeepEqual(plugins[i], p) {
		return plugins, false
	}
	out := slices.Clone(plugins)
	if i >= 0 {
		out[i] = p
		return out, true
	}
	return append(out, p), true
}
