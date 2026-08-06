package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertMCPAccess reconstructs an MCP server's access.identity_providers and
// access.metadata from the auth plugins on its route, and returns the plugins
// that are NOT MCP auth (to be handled as policies).
//
// Auth on an MCP route is always identity-provider access (the forward
// converter emits it from access.identity_providers/metadata, never as a
// policy), so — unlike agents/models — recognition does not depend on the
// synthesized "anonymous" fallback marker (MCP auth carries none):
//   - ai-mcp-oauth2      -> access.metadata + a synthesized openid-connect provider
//   - key-auth / openid-connect (no metadata) -> a synthesized provider
func (r *Reverter) revertMCPAccess(m *aigw.MCPServer, plugins []kong.Plugin) []kong.Plugin {
	var rest []kong.Plugin
	for _, p := range plugins {
		switch p.Name {
		case "ai-mcp-oauth2":
			r.applyMCPOAuth2(m, p)
		case "key-auth", "openid-connect":
			idp := r.registerIdentityProvider(p)
			m.Access.IdentityProviders = append(m.Access.IdentityProviders, idp.Name)
		default:
			rest = append(rest, p)
		}
	}
	return rest
}

// applyMCPOAuth2 lifts an ai-mcp-oauth2 plugin back into an MCP server's
// access.metadata and (when the plugin carries client credentials) a
// synthesized openid-connect identity provider.
//
// The forward converter can source authorization_servers/scopes_supported from
// either the metadata or the OIDC provider (issuer/scopes fallback); the
// reverse consistently attributes them to the metadata so re-conversion
// reproduces the same plugin. Consequently the synthesized provider is minimal
// (client credentials only) — full provider fidelity is not round-tripped, but
// the decK output is byte-identical.
func (r *Reverter) applyMCPOAuth2(m *aigw.MCPServer, p kong.Plugin) {
	cfg := p.Config

	meta := &aigw.MCPProtectedResourceMetadata{
		Endpoint:             getStr(cfg, "metadata_endpoint"),
		Resource:             getStr(cfg, "resource"),
		AuthorizationServers: toStrings(cfg["authorization_servers"]),
		ScopesSupported:      toStrings(cfg["scopes_supported"]),
	}
	m.Access.Metadata = meta

	// The forward converter appends metadata_endpoint to the route's paths so
	// one route serves both MCP traffic and the .well-known metadata; strip it
	// back out here.
	if meta.Endpoint != "" {
		m.Config.Route.Paths = removeFirst(m.Config.Route.Paths, meta.Endpoint)
	}

	oidcCfg := map[string]any{}
	if id := getStr(cfg, "client_id"); id != "" {
		oidcCfg["client_id"] = []any{id}
	}
	if secret := getStr(cfg, "client_secret"); secret != "" {
		oidcCfg["client_secret"] = []any{secret}
	}
	if v := getBool(cfg, "ssl_verify"); v != nil {
		oidcCfg["ssl_verify"] = *v
	}
	if len(oidcCfg) == 0 {
		// Metadata-only (no client credentials): no identity provider to
		// reconstruct.
		return
	}
	idp := r.registerIdentityProvider(kong.Plugin{Name: "openid-connect", Config: oidcCfg})
	m.Access.IdentityProviders = append(m.Access.IdentityProviders, idp.Name)
}

// removeFirst returns paths with the first occurrence of target removed.
func removeFirst(paths []string, target string) []string {
	for i, p := range paths {
		if p == target {
			return append(paths[:i:i], paths[i+1:]...)
		}
	}
	return paths
}
