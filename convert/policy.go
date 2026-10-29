package convert

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// convertGlobalPolicies emits each global policy as a top-level (global) Kong
// plugin. Non-global policies are instantiated per referencing entity instead.
func (c *Converter) convertGlobalPolicies() {
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		if p.Global == nil || !*p.Global {
			continue
		}
		src := issueSource(kindPolicy, p.Name)
		c.convertEntity(src, func() error {
			if plugin, ok := c.policyPlugin(p, c.labelsToTags(p.Labels), true); ok {
				c.out.Plugins = append(c.out.Plugins, plugin)
			}
			return nil
		})
	}
}

// warnGlobalPoliciesForWebSocket flags every non-authentication global policy
// once the document emits a WebSocket route. A global plugin carries no
// protocols override, so it keeps Kong's schema default (http/https) and never
// runs against ws/wss traffic. convertModels already left out every WebSocket
// model when a global authentication policy exists.
func (c *Converter) warnGlobalPoliciesForWebSocket() {
	if !c.hasWebSocketRoute() {
		return
	}
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		if p.Global == nil || !*p.Global || authPolicyTypes[p.Type] {
			continue
		}
		c.warn(issueSource(kindPolicy, p.Name).at("global"), BehaviorMayDiffer,
			"global policy %q (type %q) keeps Kong's http/https default protocols, "+
				"so it will not run on the WebSocket routes this document emits",
			p.Name, p.Type)
	}
}

// globalAuthPolicy returns a global authentication policy, or nil.
func (c *Converter) globalAuthPolicy() *aigw.Policy {
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		if p.Global != nil && *p.Global && authPolicyTypes[p.Type] {
			return p
		}
	}
	return nil
}

// hasWebSocketRoute reports whether any converted model route moved to the
// WebSocket Service.
func (c *Converter) hasWebSocketRoute() bool {
	for _, svc := range c.out.Services {
		if svc.Name == aimap.GatewayWebSocketServiceName && len(svc.Routes) > 0 {
			return true
		}
	}
	return false
}

// authPolicyTypes are policy types that must be configured as identity
// providers (scoped authentication with anonymous fallback) rather than as
// plain policies, when referenced from a model.
var authPolicyTypes = map[string]bool{
	"key-auth":       true,
	"openid-connect": true,
}

// scopedPlugins builds the plugins to nest under the referencing entity src:
// one per non-global policy reference, plus an acl plugin when ACLs are
// present. A policy that cannot be converted is left out with an Error issue.
// An unrepresentable access control fails the entity.
func (c *Converter) scopedPlugins(src ConversionIssueSource, refs []string, acls aigw.ACLs) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for i, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		field := fmt.Sprintf("policies[%d]", i)
		p := c.policies[ref]
		if p == nil {
			c.reportError(src.at(field), UndefinedReference, "unknown policy reference %q", ref)
			continue
		}
		if src.Kind == kindModel && authPolicyTypes[p.Type] {
			return nil, c.fail(src.at(field), ConfigFieldInvalid,
				"model policy %q has type %q, but authentication policies can only "+
					"be applied to models via auth_strategies, not policies",
				ref, p.Type)
		}
		if p.Global != nil && *p.Global {
			continue // emitted once at the top level
		}
		if plugin, ok := c.policyPlugin(p, nil, false); ok {
			plugins = append(plugins, plugin)
		}
	}
	if !acls.IsEmpty() {
		// A Kong acl plugin enforces only_one_of {config.allow, config.deny,
		// config.allow_when, config.deny_when}; an AI Gateway acl that sets more
		// than one is not representable as one valid plugin, so reject it rather
		// than emit config the gateway will refuse to load.
		if set := aclFieldPaths("", acls); len(set) > 1 {
			return nil, c.fail(src.at(aclFieldPaths("access.acls.", acls)...), ConfigFieldInvalid,
				"acl policy sets %s, but a Kong acl plugin permits exactly one of "+
					"allow, deny, allow_when, or deny_when", strings.Join(set, " and "))
		}
		if fields := celFieldPaths("access.acls.", acls); len(fields) > 0 && !c.supports(aimap.FeatureACLWhen) {
			return nil, c.failUnsupported(src.at(fields...), aimap.FeatureACLWhen, fields[0])
		}
		plugins = append(plugins, aclPlugin(acls))
	}
	return plugins, nil
}

// policyPlugin builds the plugin for p. It reports false when p cannot be
// converted. The Error issue names the policy.
func (c *Converter) policyPlugin(p *aigw.Policy, tags []string, preserveID bool) (kong.Plugin, bool) {
	// Without its condition, the plugin runs on every request.
	if p.Condition != "" && !c.supports(aimap.FeaturePolicyCondition) {
		c.reportUnsupported(issueSource(kindPolicy, p.Name).at("condition"), Error,
			aimap.FeaturePolicyCondition, "condition")
		return kong.Plugin{}, false
	}
	if len(p.Expressions) > 0 && !c.supports(aimap.FeaturePolicyExpressions) {
		c.reportUnsupported(issueSource(kindPolicy, p.Name).at("expressions"), Error,
			aimap.FeaturePolicyExpressions, "expressions")
		return kong.Plugin{}, false
	}
	config, ok := c.applyDatastore(p, c.normalizeRateLimitingProviderMatches(p))
	if !ok {
		return kong.Plugin{}, false
	}
	plugin := kong.Plugin{
		Name:        p.Type,
		Condition:   p.Condition,
		Expressions: p.Expressions,
		Config:      config,
		Tags:        tags,
		Source:      source("policy", p.Name, "config"),
	}
	if preserveID {
		plugin.ID = p.ID
	}
	if p.Enabled != nil && !*p.Enabled {
		disabled := false
		plugin.Enabled = &disabled
	}
	return plugin, true
}

// applyDatastore hands p's datastore references, and the registry to resolve
// them against, to aimap.ApplyDatastore, which owns every rule about the
// pairing. A dangling reference keeps the policy without a connection.
func (c *Converter) applyDatastore(p *aigw.Policy, config map[string]any) (map[string]any, bool) {
	if len(p.Datastores) == 0 {
		return config, true
	}
	src := issueSource(kindPolicy, p.Name)
	out, err := aimap.ApplyDatastore(config, p.Type, p.Datastores, c.datastores)
	var unknown *aimap.UnknownDatastoreError
	switch {
	case errors.As(err, &unknown):
		// aimap returns config untouched on this path, so the policy still
		// converts, just without a connection.
		c.reportError(src.at(datastoreField(p.Datastores, unknown.Name)), UndefinedReference,
			"policy %q %v", p.Name, unknown)
		return out, true
	case err != nil:
		c.reportError(src.at("datastores"), ConfigFieldInvalid, "policy %q: %v", p.Name, err)
		return nil, false
	}
	return out, true
}

// datastoreField returns the path of the reference to name in refs.
func datastoreField(refs []aigw.DatastoreRef, name string) string {
	for i, ref := range refs {
		if ref.Name == name {
			return fmt.Sprintf("datastores[%d].name", i)
		}
	}
	return "datastores"
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
