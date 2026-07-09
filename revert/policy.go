package revert

import (
	"fmt"
	"reflect"

	"github.com/Kong/ai-deck-converter/internal/aigw"
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
		if !reflect.DeepEqual(existing.Config, p.Config) {
			continue
		}
		return existing
	}

	policy := aigw.Policy{
		Type:    p.Name,
		Name:    r.uniquePolicyName(p.Name),
		Enabled: p.Enabled,
		Config:  p.Config,
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
