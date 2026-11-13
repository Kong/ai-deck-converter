package revert

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// aiPluginNames are plugins reconstructed into first-class AI Gateway entities
// rather than policies; they are skipped by policyRefs.
var aiPluginNames = map[string]bool{
	"ai-proxy-advanced": true,
	"ai-model-selector": true,
	"ai-mcp-proxy":      true,
	"ai-a2a-proxy":      true,
}

// revertGlobalPolicies converts each top-level unscoped plugin into a global
// AI Gateway policy.
func (r *Reverter) revertGlobalPolicies() {
	for _, p := range r.idx.global {
		policy := r.registerPolicy(p, true)
		policy.Labels = r.tagsToLabels(p.Tags)
	}
}

// authPluginNames are authentication plugins reconstructed into identity
// providers (with an entity's access.auth_strategies reference) rather than
// plain policies when found on a Model or Agent route.
var authPluginNames = map[string]bool{
	"key-auth":       true,
	"openid-connect": true,
}

// pluginType returns the bundled plugin a clone copies, so every dispatch on a
// plugin name sees the plugin the user configured. A clone the document does not
// declare keeps its own name.
func (r *Reverter) pluginType(p kong.Plugin) string {
	if ref, ok := r.idx.cloneRefs[p.Name]; ok {
		return ref
	}
	if ref, ok := aimap.CloneRef(p.Name); ok {
		return ref
	}
	return p.Name
}

// policyRefs converts an entity's plugins into policy name references,
// special-casing acl plugins into ACLs and skipping the AI plugins (which are
// reconstructed into first-class entities by the service reversal steps).
func (r *Reverter) policyRefs(plugins []kong.Plugin) ([]string, aigw.ACLs) {
	var refs []string
	var acls aigw.ACLs
	for _, p := range plugins {
		switch pluginType := r.pluginType(p); {
		case aiPluginNames[pluginType]:
			continue
		case pluginType == "acl":
			acls = aclsFromBlock(p.Config)
		default:
			refs = append(refs, r.registerPolicy(p, false).Name)
		}
	}
	return refs, acls
}

// authStrategyPolicyRefs is policyRefs plus auth-strategy recovery: it
// pulls key-auth/openid-connect plugins out into access.auth_strategies
// references
// before delegating the remaining plugins to policyRefs.
func (r *Reverter) authStrategyPolicyRefs(plugins []kong.Plugin) ([]string, aigw.ACLs, []string) {
	plugins = slices.Clone(plugins)
	for i := range plugins {
		if r.pluginType(plugins[i]) == "openid-connect" {
			plugins[i].Config = withoutHiddenAccessTokenHeader(plugins[i].Config)
		}
	}
	return r.splitAuthStrategies(plugins, func(p kong.Plugin) bool {
		// The anonymous fallback is the forward converter's marker that an
		// auth plugin originated from an auth strategy. A bare key-auth/OIDC
		// plugin with http/https protocols remains a regular policy; one
		// carrying ws/wss is recovered as an auth strategy, like the forward
		// converter's own WebSocket auth plugins.
		return p.Config["anonymous"] == anonymousConsumerName || isWebSocketAuthPlugin(p)
	})
}

// splitAuthStrategies recovers each auth plugin for which isStrategy is true
// as an auth strategy reference. It changes the other plugins into policies.
func (r *Reverter) splitAuthStrategies(
	plugins []kong.Plugin, isStrategy func(kong.Plugin) bool,
) ([]string, aigw.ACLs, []string) {
	var rest []kong.Plugin
	var idpRefs []string
	for _, p := range plugins {
		if idpType := r.pluginType(p); authPluginNames[idpType] && isStrategy(p) {
			idpRefs = append(idpRefs, r.registerAuthStrategy(p, idpType).Name)
			continue
		}
		rest = append(rest, p)
	}
	refs, acls := r.policyRefs(rest)
	return refs, acls, idpRefs
}

// isWebSocketAuthPlugin reports whether p is an auth plugin that runs on
// WebSocket routes with no anonymous fallback. The forward converter drops the
// fallback there. request-termination does not run on ws and wss.
func isWebSocketAuthPlugin(p kong.Plugin) bool {
	_, anonymous := p.Config["anonymous"]
	return !anonymous && hasWebSocketProtocol(p.Protocols)
}

func hasWebSocketProtocol(protocols []string) bool {
	return slices.ContainsFunc(protocols, func(p string) bool { return p == "ws" || p == "wss" })
}

// modelPolicyRefs recovers every auth plugin of a model as an auth strategy.
// A model policy cannot be an auth plugin. A model route with one auth
// strategy has no anonymous fallback.
func (r *Reverter) modelPolicyRefs(plugins []kong.Plugin) ([]string, aigw.ACLs, []string) {
	return r.splitAuthStrategies(plugins, func(kong.Plugin) bool { return true })
}

// registerPolicy reuses a registered policy with equal type, scope kind,
// enabled state, condition, expressions, and config. Otherwise it registers a
// new policy with a unique name.
//
// A cloned plugin reverts as the policy type of the plugin it copies: the clone
// exists only to give one instance another priority, and the AI Gateway model
// has no clone. The document's cloned_plugins names the base plugin, so a
// user-authored clone resolves too, not only the converter's fixed names.
func (r *Reverter) registerPolicy(p kong.Plugin, global bool) *aigw.Policy {
	policyType := r.pluginType(p)
	for i := range r.policies {
		existing := &r.policies[i]
		if existing.Type != policyType {
			continue
		}
		if (existing.Global != nil && *existing.Global) != global {
			continue
		}
		if !boolPtrEqual(existing.Enabled, p.Enabled) {
			continue
		}
		if existing.Condition != p.Condition {
			continue
		}
		if !reflect.DeepEqual(existing.Expressions, p.Expressions) {
			continue
		}
		if !reflect.DeepEqual(existing.Config, p.Config) {
			continue
		}
		return existing
	}

	policy := aigw.Policy{
		Type:        policyType,
		Name:        r.uniquePolicyName(policyType),
		Enabled:     p.Enabled,
		Condition:   p.Condition,
		Expressions: p.Expressions,
		Config:      p.Config,
	}
	if global {
		policy.Global = boolPtr(true)
	}
	r.policies = append(r.policies, policy)
	return &r.policies[len(r.policies)-1]
}

// uniquePolicyName returns base, or base-N on collision.
func (r *Reverter) uniquePolicyName(base string) string {
	name := base
	for n := 2; r.policyNames[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	r.policyNames[name] = true
	return name
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
