package revert

import (
	"fmt"
	"reflect"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/Kong/ai-deck-converter/internal/policies"
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
// providers (with a model's access.identity_providers reference) rather than
// plain policies, but only when found on a model's route — see
// modelPolicyRefs. On every other entity they remain plain policies, since
// only models support identity providers.
var authPluginNames = map[string]bool{
	"key-auth":       true,
	"openid-connect": true,
}

// policyRefs converts an entity's plugins into policy name references,
// special-casing acl plugins into ACLs and skipping the AI plugins (which are
// reconstructed into first-class entities by the service reversal steps).
func (r *Reverter) policyRefs(plugins []kong.Plugin) ([]string, aigw.ACLs) {
	var refs []string
	var acls aigw.ACLs
	for _, p := range plugins {
		switch {
		case aiPluginNames[p.Name]:
			continue
		case p.Name == "acl":
			acls = aclsFromBlock(p.Config)
		default:
			refs = append(refs, r.registerPolicy(p, false).Name)
		}
	}
	return refs, acls
}

// modelPolicyRefs is policyRefs plus identity-provider recovery: it pulls
// key-auth/openid-connect plugins out into identity provider references
// before delegating the remaining plugins to policyRefs. Used only for a
// model's route/model-scoped guard plugins, since only models support
// identity providers.
func (r *Reverter) modelPolicyRefs(plugins []kong.Plugin) ([]string, aigw.ACLs, []string) {
	var rest []kong.Plugin
	var idpRefs []string
	for _, p := range plugins {
		if authPluginNames[p.Name] {
			idpRefs = append(idpRefs, r.registerIdentityProvider(p).Name)
			continue
		}
		rest = append(rest, p)
	}
	refs, acls := r.policyRefs(rest)
	return refs, acls, idpRefs
}

// registerPolicy dedupes a plugin into the policy registry: a plugin with the
// same type, config, enabled state, and scope kind reuses the existing policy;
// otherwise a new policy is registered under a unique name.
func (r *Reverter) registerPolicy(p kong.Plugin, global bool) *aigw.Policy {
	// Reshape the 3.15 ai-rate-limiting-advanced llm_providers shape into the
	// AI Gateway 2.0 config.policies shape before the field-drop below removes
	// the now-consumed llm_providers/llm_format. Operates on a copy so the
	// shared p.Config map is never mutated.
	src := p.Config
	if p.Name == "ai-rate-limiting-advanced" {
		src = reshapeAIRateLimiting(src)
	}

	// Drop fields/enum values that exist in the API Gateway 3.15 plugin schema
	// but are rejected by the AI Gateway 2.0 policy schema, so the migrated
	// policy validates. The shared p.Config map is never mutated.
	cfg := policies.SanitizeConfig(p.Name, src)

	for i := range r.policies {
		existing := &r.policies[i]
		if existing.Type != p.Name {
			continue
		}
		if (existing.Global != nil && *existing.Global) != global {
			continue
		}
		if !boolPtrEqual(existing.Enabled, p.Enabled) {
			continue
		}
		if !reflect.DeepEqual(existing.Config, cfg) {
			continue
		}
		return existing
	}

	policy := aigw.Policy{
		Type:    p.Name,
		Name:    r.uniquePolicyName(p.Name),
		Enabled: p.Enabled,
		Config:  cfg,
	}
	if global {
		policy.Global = boolPtr(true)
	}
	r.policies = append(r.policies, policy)
	return &r.policies[len(r.policies)-1]
}

// reshapeAIRateLimiting rewrites the API Gateway 3.15 ai-rate-limiting-advanced
// per-provider limit shape (config.llm_providers: [{name, limit: [...],
// window_size: [...]}]) into the AI Gateway 2.0 config.policies shape
// (config.policies: [{match: [{type: provider, values: [name]}], limits:
// [{limit, window_size, tokens_count_strategy}]}]). limit and window_size are
// parallel arrays; tokens_count_strategy is lifted from the top-level config.
//
// Only configs that actually carry a populated llm_providers list are reshaped;
// when it is null/absent (e.g. instances already authored with config.policies)
// the config is returned unchanged. The returned map is a shallow copy when
// reshaped, so the caller's p.Config is never mutated (the following
// policies.SanitizeConfig then drops the consumed llm_providers/llm_format).
func reshapeAIRateLimiting(cfg map[string]any) map[string]any {
	provs := getSlice(cfg, "llm_providers")
	if len(provs) == 0 {
		return cfg
	}
	tcs := cfg["tokens_count_strategy"]

	var out []any
	for _, raw := range provs {
		pr, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		limits := getSlice(pr, "limit")
		windows := getSlice(pr, "window_size")
		var lims []any
		for i, lim := range limits {
			entry := map[string]any{"limit": lim, "tokens_count_strategy": tcs}
			if i < len(windows) {
				entry["window_size"] = windows[i]
			}
			lims = append(lims, entry)
		}
		out = append(out, map[string]any{
			"match":  []any{map[string]any{"type": "provider", "values": []any{pr["name"]}}},
			"limits": lims,
		})
	}
	if len(out) == 0 {
		return cfg
	}

	nc := make(map[string]any, len(cfg)+1)
	for k, v := range cfg {
		nc[k] = v
	}
	nc["policies"] = out
	return nc
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
