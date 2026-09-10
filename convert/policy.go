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

// datastoreVectorDBPolicyTypes are the policy plugin types that accept a
// Datastore expansion into their config's vectordb block, per
// docs/spec/koko-4450_datastore_policy.md: every AI Gateway plugin that
// carries a vectordb+embeddings partial. The ticket's other group — the
// model/llm-partial plugins (ai-llm-as-judge, ai-request-transformer,
// ai-response-transformer, ai-proxy) — reference model/provider credentials
// at config.llm.model/config.model instead, a different resource than
// Datastore models, and isn't wired here.
var datastoreVectorDBPolicyTypes = map[string]bool{
	"ai-rag-injector":            true,
	"ai-semantic-cache":          true,
	"ai-semantic-prompt-guard":   true,
	"ai-semantic-response-guard": true,
}

// applyDatastore expands p.Datastore into config's vectordb block, when the
// policy type supports it. The connection sub-block the Datastore's type maps
// to (config.vectordb.redis or .pgvector) is replaced wholesale; every other
// key already in config.vectordb — the common fields (strategy, dimensions,
// distance_metric, threshold) a policy or model authors itself, per
// VectorDBDatastoreConfig's own doc comment in the source API spec — is left
// untouched. config is never the caller's only reference to a live map (see
// normalizeRateLimitingProviderMatches's own copy-on-write note): a fresh
// config and a fresh vectordb block are allocated whenever an expansion
// actually happens, so a reusable source Policy is never mutated.
func (c *Converter) applyDatastore(p *aigw.Policy, config map[string]any) (map[string]any, error) {
	if p.Datastore == nil {
		return config, nil
	}
	if !datastoreVectorDBPolicyTypes[p.Type] {
		if err := c.warn("policy %q references a datastore, "+
			"but plugin type %q does not support datastore config expansion", p.Name, p.Type); err != nil {
			return nil, err
		}
		return config, nil
	}
	vectorDB, ok := aimap.VectorDBForDatastoreType(p.Datastore.Type)
	if !ok {
		if err := c.warn("policy %q references a datastore of unrecognized type %q", p.Name, p.Datastore.Type); err != nil {
			return nil, err
		}
		return config, nil
	}
	if vectorDB == "redis" && p.Datastore.Type != "redis-ee" {
		return nil, c.failAt("policies",
			"policy %q's plugin type %q only supports a redis-ee datastore, got %q",
			p.Name, p.Type, p.Datastore.Type)
	}

	pluginConfigMap := make(map[string]any, len(config)+1)
	for k, v := range config {
		pluginConfigMap[k] = v
	}
	vectordb := map[string]any{}
	if existing, ok := pluginConfigMap["vectordb"].(map[string]any); ok {
		for k, v := range existing {
			if k != vectorDB {
				vectordb[k] = v
			}
		}
	}
	vectordb[vectorDB] = p.Datastore.Config
	pluginConfigMap["vectordb"] = vectordb
	return pluginConfigMap, nil
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
