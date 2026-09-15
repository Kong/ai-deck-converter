package convert

import (
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// convertGlobalPolicies emits each global policy as a top-level (global) Kong
// plugin. Non-global policies are instantiated per referencing entity instead.
func (c *Converter) convertGlobalPolicies() error {
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		if p.Global != nil && *p.Global {
			plugin, err := c.policyPlugin(p, c.labelsToTags(p.Labels), true)
			if err != nil {
				return err
			}
			c.out.Plugins = append(c.out.Plugins, plugin)
		}
	}
	return nil
}

// entityKind identifies the kind of entity scopedPlugins is building plugins
// for, so it can apply entity-specific validation (e.g. rejecting
// authentication policies on models).
const (
	entityModel         = "model"
	entityMCPServer     = "mcp_server"
	entityAgent         = "agent"
	entityConsumer      = "consumer"
	entityConsumerGroup = "consumer_group"
)

// authPolicyTypes are policy types that must be configured as identity
// providers (scoped authentication with anonymous fallback) rather than as
// plain policies, when referenced from a model.
var authPolicyTypes = map[string]bool{
	"key-auth":       true,
	"openid-connect": true,
}

// scopedPlugins builds the plugins to nest under a referencing entity: one per
// non-global policy reference, plus an acl plugin when ACLs are present.
func (c *Converter) scopedPlugins(entityKind string, refs []string, acls aigw.ACLs) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		p := c.policies[ref]
		if p == nil {
			if err := c.warn("unknown policy reference %q", ref); err != nil {
				return nil, err
			}
			continue
		}
		if entityKind == entityModel && authPolicyTypes[p.Type] {
			return nil, c.failAt("policies",
				"model policy %q has type %q, but authentication policies can only "+
					"be applied to models via auth_strategies, not policies",
				ref, p.Type)
		}
		if p.Global != nil && *p.Global {
			continue // emitted once at the top level
		}
		plugin, err := c.policyPlugin(p, nil, false)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	if !acls.IsEmpty() {
		// A Kong acl plugin enforces only_one_of {config.allow, config.deny}; an
		// AI Gateway acl that sets both is not representable as one valid plugin,
		// so reject it rather than emit config the gateway will refuse to load.
		if len(acls.Allow) > 0 && len(acls.Deny) > 0 {
			return nil, c.failAt("access.acls",
				"acl policy sets both allow (%v) and deny (%v), but a Kong acl plugin permits "+
					"exactly one; set only allow or only deny", acls.Allow, acls.Deny)
		}
		plugins = append(plugins, aclPlugin(acls))
	}
	return plugins, nil
}

func (c *Converter) policyPlugin(p *aigw.Policy, tags []string, preserveID bool) (kong.Plugin, error) {
	config, err := c.applyDatastore(p, c.normalizeRateLimitingProviderMatches(p))
	if err != nil {
		return kong.Plugin{}, err
	}
	plugin := kong.Plugin{
		Name:   p.Type,
		Config: config,
		Tags:   tags,
		Source: source("policy", p.Name, "config"),
	}
	if preserveID {
		plugin.ID = p.ID
	}
	if p.Enabled != nil && !*p.Enabled {
		disabled := false
		plugin.Enabled = &disabled
	}
	return plugin, nil
}

// applyDatastore resolves p.Datastore (a by-name reference into the top-level
// datastores list) and hands the connection to aimap.ApplyDatastore, which
// decides where in the plugin config it lands. p.Datastore is a list for
// parity with Kong's plugin schema (partials) but holds at most one entry.
func (c *Converter) applyDatastore(p *aigw.Policy, config map[string]any) (map[string]any, error) {
	if len(p.Datastores) == 0 {
		return config, nil
	}
	if len(p.Datastores) > 1 {
		return nil, c.failAt("policies",
			"policy %q has %d datastores, but only one is allowed",
			p.Name, len(p.Datastores))
	}
	ds := c.datastores[p.Datastores[0].Name]
	if ds == nil {
		if err := c.warn("policy %q references unknown datastore %q", p.Name, p.Datastores[0].Name); err != nil {
			return nil, err
		}
		return config, nil
	}
	out, err := aimap.ApplyDatastore(config, p.Type, ds.Type, ds.Config)
	if err != nil {
		return nil, c.failAt("policies", "policy %q's %v", p.Name, err)
	}
	return out, nil
}

// normalizeRateLimitingProviderMatches lowers AI Gateway model-provider entity
// names to the provider types that ai-rate-limiting-advanced sees at runtime.
// The data plane only receives target.model.provider (for example "openai"),
// not the source model-provider name used to resolve auth and options.
//
// Native provider literals and glob patterns are left untouched. A shallow copy
// is sufficient for unchanged branches; every branch we modify is copied before
// writing so a reusable source Policy is never mutated while it is emitted at
// several scopes.
func (c *Converter) normalizeRateLimitingProviderMatches(p *aigw.Policy) map[string]any {
	if p.Type != "ai-rate-limiting-advanced" || len(p.Config) == 0 {
		return p.Config
	}

	policies, ok := p.Config["policies"].([]any)
	if !ok {
		return p.Config
	}

	config := make(map[string]any, len(p.Config))
	for key, value := range p.Config {
		config[key] = value
	}
	convertedPolicies := append([]any(nil), policies...)
	changed := false

	for policyIndex, rawPolicy := range policies {
		policy, ok := rawPolicy.(map[string]any)
		if !ok {
			continue
		}
		matches, ok := policy["match"].([]any)
		if !ok {
			continue
		}

		convertedMatches := append([]any(nil), matches...)
		policyChanged := false
		for matchIndex, rawMatch := range matches {
			match, ok := rawMatch.(map[string]any)
			if !ok || match["type"] != "provider" {
				continue
			}
			values, ok := match["values"].([]any)
			if !ok {
				continue
			}

			convertedValues := append([]any(nil), values...)
			matchChanged := false
			for valueIndex, rawValue := range values {
				value, ok := rawValue.(string)
				if !ok || strings.ContainsAny(value, "*?") {
					continue
				}
				provider := c.providers[value]
				if provider == nil || provider.Type == "" || provider.Type == value {
					continue
				}
				convertedValues[valueIndex] = provider.Type
				matchChanged = true
			}
			if !matchChanged {
				continue
			}

			convertedMatch := make(map[string]any, len(match))
			for key, value := range match {
				convertedMatch[key] = value
			}
			convertedMatch["values"] = convertedValues
			convertedMatches[matchIndex] = convertedMatch
			policyChanged = true
		}
		if !policyChanged {
			continue
		}

		convertedPolicy := make(map[string]any, len(policy))
		for key, value := range policy {
			convertedPolicy[key] = value
		}
		convertedPolicy["match"] = convertedMatches
		convertedPolicies[policyIndex] = convertedPolicy
		changed = true
	}

	if !changed {
		return p.Config
	}
	config["policies"] = convertedPolicies
	return config
}
