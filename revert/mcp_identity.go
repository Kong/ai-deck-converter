package revert

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
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

	oidcCfg := oidcConfigFromOAuth2(cfg)
	if len(oidcCfg) == 0 {
		// Metadata-only (no identity-provider fields): no identity provider to
		// reconstruct.
		return
	}
	idp := r.registerIdentityProvider(kong.Plugin{Name: "openid-connect", Config: oidcCfg})
	m.Access.IdentityProviders = append(m.Access.IdentityProviders, idp.Name)
}

// oidcConfigFromOAuth2 lifts an ai-mcp-oauth2 plugin config back into an
// openid-connect identity provider config, inverting
// convert's applyOIDCFieldsToOAuth2 via aimap's shared field table. The array-
// first kinds re-wrap the plugin's scalar/path into the one-element array the
// forward converter collapses; this is lossy for multi-element sources but
// re-converts to the same plugin (see revert/roundtrip_test.go).
func oidcConfigFromOAuth2(cfg map[string]any) map[string]any {
	oidcCfg := map[string]any{}
	for _, f := range aimap.OIDCToMCPOAuth2Fields {
		switch f.Kind {
		case aimap.OAuth2Passthrough:
			if v, ok := cfg[f.OAuth2Key]; ok {
				oidcCfg[f.OIDCKey] = v
			}
		case aimap.OAuth2FirstOfArray:
			if s := getStr(cfg, f.OAuth2Key); s != "" {
				oidcCfg[f.OIDCKey] = []any{s}
			}
		case aimap.OAuth2FirstOfPaths:
			if p := getSlice(cfg, f.OAuth2Key); len(p) > 0 {
				oidcCfg[f.OIDCKey] = []any{p}
			}
		}
	}
	for key, value := range oidcProxyFields(getMap(cfg, "proxy_config")) {
		oidcCfg[key] = value
	}

	// passthrough_credentials: true <=> the provider disabled hide_credentials.
	if p := getBool(cfg, "passthrough_credentials"); p != nil && *p {
		oidcCfg["hide_credentials"] = false
	}
	// insecure_relaxed_audience_validation: false <=> the provider required an
	// audience; reconstruct audience_required from the resource (its RFC 8707
	// audience). The concrete audience list is lossy but re-converts to the
	// same plugin flag. The relaxed default (true) leaves audience_required off.
	if v := getBool(cfg, "insecure_relaxed_audience_validation"); v != nil && !*v {
		if resource := getStr(cfg, "resource"); resource != "" {
			oidcCfg["audience_required"] = []any{resource}
		}
	}

	return oidcCfg
}

// oidcProxyFields reconstructs openid-connect's legacy proxy fields from the
// shared ai-mcp-oauth2 proxy_config record. proxy_config has one scheme and
// one credential pair, so both reconstructed authorization headers are
// necessarily identical.
func oidcProxyFields(proxyConfig map[string]any) map[string]any {
	if len(proxyConfig) == 0 {
		return nil
	}

	oidcFields := map[string]any{}
	scheme := getStr(proxyConfig, "proxy_scheme")
	if scheme == "http" || scheme == "https" {
		if raw := proxyURL(
			scheme, getStr(proxyConfig, "http_proxy_host"), getInt(proxyConfig, "http_proxy_port"),
		); raw != "" {
			oidcFields["http_proxy"] = raw
		}
		if raw := proxyURL(
			scheme, getStr(proxyConfig, "https_proxy_host"), getInt(proxyConfig, "https_proxy_port"),
		); raw != "" {
			oidcFields["https_proxy"] = raw
		}
	}
	if noProxy := getStr(proxyConfig, "no_proxy"); noProxy != "" {
		oidcFields["no_proxy"] = noProxy
	}

	username, password := getStr(proxyConfig, "auth_username"), getStr(proxyConfig, "auth_password")
	if username != "" || password != "" {
		authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if _, found := oidcFields["http_proxy"]; found {
			oidcFields["http_proxy_authorization"] = authorization
		}
		if _, found := oidcFields["https_proxy"]; found {
			oidcFields["https_proxy_authorization"] = authorization
		}
	}
	return oidcFields
}

func proxyURL(scheme, host string, port *int) string {
	if host == "" {
		return ""
	}
	if port != nil {
		host = net.JoinHostPort(host, strconv.Itoa(*port))
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: scheme, Host: host}).String()
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
