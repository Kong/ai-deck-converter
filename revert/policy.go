package revert

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

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
// scope is the AI Gateway scope of the owning entity (models, mcp-servers,
// agents, consumers, consumer-groups); a plugin whose type is not permitted in
// that scope is dropped with a warning, since the API would reject it.
func (r *Reverter) policyRefs(plugins []kong.Plugin, scope string) ([]string, aigw.ACLs) {
	var refs []string
	var acls aigw.ACLs
	for _, p := range plugins {
		switch {
		case aiPluginNames[p.Name]:
			continue
		case p.Name == "acl":
			acls = aclsFromBlock(p.Config)
		case !policies.PolicyAllowedInScope(p.Name, scope):
			r.warn("policy %q (type %q) is not supported on scope %q; dropped", //nolint:errcheck
				p.Name, p.Name, scope)
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
	refs, acls := r.policyRefs(rest, "models")
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
	if p.Name == "ai-mcp-oauth2" {
		src = r.reshapeMCPOAuth2Proxy(src)
	}
	if p.Name == "ai-request-transformer" || p.Name == "ai-response-transformer" {
		src = reshapeTransformerProxy(src)
	}
	if p.Name == "openid-connect" {
		src = r.reshapeOpenIDConnect(src)
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

// reshapeMCPOAuth2Proxy rewrites the API Gateway 3.15 ai-mcp-oauth2 flat
// forward-proxy fields (http_proxy/https_proxy URL strings,
// http_proxy_authorization/https_proxy_authorization, no_proxy) into the AI
// Gateway 2.0 nested config.proxy_config object (…_host/…_port split,
// auth_username/auth_password, no_proxy). Null fields are simply dropped. The
// returned map is a shallow copy, so the caller's p.Config is never mutated.
func (r *Reverter) reshapeMCPOAuth2Proxy(cfg map[string]any) map[string]any {
	flat := []string{
		"http_proxy", "https_proxy", "no_proxy",
		"http_proxy_authorization", "https_proxy_authorization",
	}
	present := false
	for _, k := range flat {
		if _, ok := cfg[k]; ok {
			present = true
			break
		}
	}
	if !present {
		return cfg
	}

	nc := make(map[string]any, len(cfg))
	for k, v := range cfg {
		nc[k] = v
	}

	pc := map[string]any{}
	if host, port := parseProxyURL(getStr(cfg, "http_proxy")); host != "" {
		pc["http_proxy_host"] = host
		if port != 0 {
			pc["http_proxy_port"] = port
		}
	}
	if host, port := parseProxyURL(getStr(cfg, "https_proxy")); host != "" {
		pc["https_proxy_host"] = host
		if port != 0 {
			pc["https_proxy_port"] = port
		}
	}
	if np := getStr(cfg, "no_proxy"); np != "" {
		pc["no_proxy"] = np
	}
	// The 3.15 *_authorization header values have no clean auth_username/
	// auth_password split in the 2.0 proxy_config; surface rather than guess.
	if getStr(cfg, "http_proxy_authorization") != "" || getStr(cfg, "https_proxy_authorization") != "" {
		r.warn("ai-mcp-oauth2 policy: proxy authorization header is not migratable to " +
			"proxy_config.auth_username/auth_password; configure it manually") //nolint:errcheck
	}

	for _, k := range flat {
		delete(nc, k)
	}
	if len(pc) > 0 {
		nc["proxy_config"] = pc
	}
	return nc
}

// reshapeTransformerProxy relocates the ai-request-transformer /
// ai-response-transformer flat forward-proxy fields (already in
// http_proxy_host/http_proxy_port form) into the AI Gateway 2.0 nested
// config.proxy_config object. Null fields are dropped. The returned map is a
// shallow copy, so the caller's p.Config is never mutated.
func reshapeTransformerProxy(cfg map[string]any) map[string]any {
	proxyFields := []string{
		"http_proxy_host", "http_proxy_port",
		"https_proxy_host", "https_proxy_port",
		"proxy_scheme", "auth_username", "auth_password", "no_proxy",
	}
	present := false
	for _, k := range proxyFields {
		if _, ok := cfg[k]; ok {
			present = true
			break
		}
	}
	if !present {
		return cfg
	}

	nc := make(map[string]any, len(cfg))
	for k, v := range cfg {
		nc[k] = v
	}
	pc := map[string]any{}
	for _, k := range proxyFields {
		if v, ok := nc[k]; ok {
			if v != nil {
				pc[k] = v
			}
			delete(nc, k)
		}
	}
	if len(pc) > 0 {
		nc["proxy_config"] = pc
	}
	return nc
}

// parseProxyURL splits a proxy address ("http://host:8080", "host:8080",
// "host") into host and port (port 0 when absent/invalid).
func parseProxyURL(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	rest := s
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	rest = strings.TrimRight(rest, "/")
	host, portStr, ok := strings.Cut(rest, ":")
	if !ok {
		return rest, 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0
	}
	return host, port
}

// oidcRemovedIn20 are openid-connect config fields present in API Gateway 3.15
// but rejected by the AI Gateway 2.0 openid-connect schema with no replacement.
var oidcRemovedIn20 = []string{
	"bearer_token_header_name", "cluster_cache_items",
	"principals", "proof_of_possession_mtls_from_header",
}

// reshapeOpenIDConnect migrates an openid-connect config from the 3.15 shape to
// AI Gateway 2.0: the flat session_redis_* fields collapse into the nested
// `redis` object (prefix stripped), consumer_claim is renamed to the plural
// consumer_claims, and the fields with no 2.0 equivalent are dropped. The
// returned map is a shallow copy, so the caller's config is never mutated.
func (r *Reverter) reshapeOpenIDConnect(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}
	nc := make(map[string]any, len(cfg))
	redis := map[string]any{}
	for k, v := range cfg {
		switch {
		case strings.HasPrefix(k, "session_redis_"):
			if v != nil {
				redis[strings.TrimPrefix(k, "session_redis_")] = v
			}
		case k == "consumer_claim":
			// Removed in 2.0. The 2.0 replacement `consumer_claims` is a list of
			// claim *paths* (list of lists); when the source already carries it,
			// keep that and drop the singular. Otherwise synthesize it by
			// wrapping each claim into a single-element path.
			if _, ok := cfg["consumer_claims"]; !ok && v != nil {
				if claims, isList := v.([]any); isList {
					wrapped := make([]any, len(claims))
					for i, c := range claims {
						wrapped[i] = []any{c}
					}
					nc["consumer_claims"] = wrapped
				}
			}
		default:
			nc[k] = v
		}
	}
	for _, k := range oidcRemovedIn20 {
		if _, ok := nc[k]; ok {
			if k == "principals" && nc[k] != nil {
				r.warn("openid-connect: `principals` config has no AI Gateway 2.0 " + //nolint:errcheck
					"equivalent and was dropped")
			}
			delete(nc, k)
		}
	}
	if len(redis) > 0 {
		nc["redis"] = redis
	}
	return nc
}

// uniquePolicyName returns base, or base-N on collision.
func (r *Reverter) uniquePolicyName(base string) string {
	name := base
	for n := 2; r.policyNames[name] || r.usedNames[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	r.policyNames[name] = true
	r.usedNames[name] = true
	return name
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
