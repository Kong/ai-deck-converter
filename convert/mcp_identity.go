package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// mcpListenerTypes are the MCP server modes that support identity-provider /
// Protected Resource Metadata based access. Other modes (conversion-only,
// upstream-server) do not front a client-facing listener the ai-mcp-oauth2
// plugin can protect.
var mcpListenerTypes = map[string]bool{
	"listener":             true,
	"conversion-listener":  true,
	"passthrough-listener": true,
}

// mcpIdentityPlugins builds the authentication plugin(s) for an MCP server's
// access.identity_providers / access.metadata, appending the metadata endpoint
// path to the route when an ai-mcp-oauth2 plugin is produced. It returns nil
// when the server declares no identity/metadata access.
//
// Unlike agents and models (convert/identityprovider.go), MCP auth never
// synthesizes an anonymous consumer / request-termination: the ai-mcp-oauth2
// plugin's consumer mapping is optional, and a bare key-auth plugin on the
// route enforces on its own.
func (c *Converter) mcpIdentityPlugins(m *aigw.MCPServer, route *kong.Route) ([]kong.Plugin, error) {
	meta := m.Access.Metadata
	if len(m.Access.IdentityProviders) == 0 && meta == nil {
		return nil, nil
	}

	// Listener guard: only listener-style modes can front the ai-mcp-oauth2
	// plugin (and its metadata endpoint). For other modes, warn and skip.
	if !mcpListenerTypes[m.Type] {
		if err := c.warn(
			"MCP server %q has type %q; access.identity_providers/metadata are only "+
				"supported for listener, conversion-listener, and passthrough-listener",
			m.Name, m.Type); err != nil {
			return nil, err
		}
		return nil, nil
	}

	idp, err := c.mcpAccessIdentityProvider(m)
	if err != nil {
		return nil, err
	}

	// key-auth cannot serve OAuth 2.0 Protected Resource Metadata; reject the
	// combination unconditionally (independent of -strict).
	if meta != nil && idp != nil && idp.Type == "key-auth" {
		return nil, c.failAt("access.metadata",
			"MCP server %q references key-auth identity provider %q with access.metadata; "+
				"OAuth Protected Resource Metadata is not supported for key-auth",
			m.Name, idp.Name)
	}

	if meta != nil {
		plugin, err := c.mcpOAuth2Plugin(m, idp, meta)
		if err != nil {
			return nil, err
		}
		if meta.Endpoint != "" {
			route.Paths = append(route.Paths, meta.Endpoint)
		}
		plugin.Source = mcpOAuth2Source(m, idp, meta)
		return []kong.Plugin{plugin}, nil
	}

	// No metadata: emit a plain passthrough auth plugin (key-auth or
	// openid-connect) named after the identity provider type.
	if idp == nil {
		return nil, nil
	}
	cfg := make(map[string]any, len(idp.Config))
	for k, v := range idp.Config {
		cfg[k] = v
	}
	plugin := kong.Plugin{Name: idp.Type, Config: cfg, Source: source("identity_provider", idp.Name, "config")}
	return []kong.Plugin{plugin}, nil
}

func mcpOAuth2Source(
	m *aigw.MCPServer,
	idp *aigw.IdentityProvider,
	meta *aigw.MCPProtectedResourceMetadata,
) *kong.Source {
	authorizationServersSource := "access.metadata.authorization_servers"
	if len(meta.AuthorizationServers) == 0 && idp != nil && firstConfigString(idp.Config, "issuer") != "" {
		authorizationServersSource = "access.identity_providers"
	}
	scopesSource := "access.metadata.scopes_supported"
	if len(meta.ScopesSupported) == 0 && idp != nil && len(configStrings(idp.Config, "scopes")) > 0 {
		scopesSource = "access.identity_providers"
	}
	return source("mcp_server", m.Name, "access.metadata",
		kong.FieldMapping{GeneratedPrefix: "config.resource", SourcePrefix: "access.metadata.resource"},
		kong.FieldMapping{GeneratedPrefix: "config.authorization_servers", SourcePrefix: authorizationServersSource},
		kong.FieldMapping{GeneratedPrefix: "config.scopes_supported", SourcePrefix: scopesSource},
		kong.FieldMapping{GeneratedPrefix: "config.metadata_endpoint", SourcePrefix: "access.metadata.endpoint"},
		kong.FieldMapping{
			GeneratedPrefix: "config.metadata_discovery_endpoint",
			SourcePrefix:    "access.metadata.discovery_endpoint",
		},
		kong.FieldMapping{GeneratedPrefix: "config.client_id", SourcePrefix: "access.identity_providers"},
		kong.FieldMapping{GeneratedPrefix: "config.client_secret", SourcePrefix: "access.identity_providers"},
		kong.FieldMapping{GeneratedPrefix: "config.ssl_verify", SourcePrefix: "access.identity_providers"},
	)
}

// mcpAccessIdentityProvider resolves the single (schema max 1) identity provider
// referenced by an MCP server's access block, warning on unknown references.
func (c *Converter) mcpAccessIdentityProvider(m *aigw.MCPServer) (*aigw.IdentityProvider, error) {
	for _, ref := range m.Access.IdentityProviders {
		idp := c.identityProviders[ref]
		if idp == nil {
			if err := c.warn("MCP server %q references unknown identity provider %q", m.Name, ref); err != nil {
				return nil, err
			}
			continue
		}
		return idp, nil
	}
	return nil, nil
}

// mcpOAuth2Plugin builds an ai-mcp-oauth2 plugin from an MCP server's Protected
// Resource Metadata and (optional) openid-connect identity provider. Required
// plugin fields are resource and authorization_servers; the metadata is the
// primary source, with the OIDC issuer/scopes filling in when the metadata
// omits them.
func (c *Converter) mcpOAuth2Plugin(
	m *aigw.MCPServer, idp *aigw.IdentityProvider, meta *aigw.MCPProtectedResourceMetadata,
) (kong.Plugin, error) {
	cfg := map[string]any{}

	setIfNotEmpty(cfg, "resource", meta.Resource)
	if meta.Resource == "" {
		if err := c.warn("MCP server %q access.metadata has no resource; ai-mcp-oauth2 requires one", m.Name); err != nil {
			return kong.Plugin{}, err
		}
	}

	authServers := meta.AuthorizationServers
	if len(authServers) == 0 && idp != nil {
		if issuer := firstConfigString(idp.Config, "issuer"); issuer != "" {
			authServers = []string{issuer}
		}
	}
	if len(authServers) > 0 {
		cfg["authorization_servers"] = authServers
	} else if err := c.warn(
		"MCP server %q access.metadata has no authorization_servers and no issuer to fall back to; "+
			"ai-mcp-oauth2 requires authorization_servers", m.Name); err != nil {
		return kong.Plugin{}, err
	}

	scopes := meta.ScopesSupported
	if len(scopes) == 0 && idp != nil {
		scopes = configStrings(idp.Config, "scopes")
	}
	if len(scopes) > 0 {
		cfg["scopes_supported"] = scopes
	}

	setIfNotEmpty(cfg, "metadata_endpoint", meta.Endpoint)
	setIfNotEmpty(cfg, "metadata_discovery_endpoint", meta.DiscoveryEndpoint)
	if idp != nil {
		applyOIDCFieldsToOAuth2(cfg, idp.Config)
		// passthrough_credentials is the inverse of the provider's
		// hide_credentials (OIDC default true); only the non-default
		// (hide_credentials: false) is carried, as passthrough_credentials: true.
		if hide, ok := idp.Config["hide_credentials"].(bool); ok && !hide {
			cfg["passthrough_credentials"] = true
		}
	}

	// insecure_relaxed_audience_validation mirrors the provider's audience
	// enforcement: AI Gateway validates the audience only when audience_required
	// is set, so the plugin relaxes validation (true) unless the provider
	// required one. Always emitted (see aimap.OIDCToMCPOAuth2Fields doc).
	cfg["insecure_relaxed_audience_validation"] = !oidcRequiresAudience(idp)

	return kong.Plugin{Name: "ai-mcp-oauth2", Config: cfg}, nil
}

// oidcRequiresAudience reports whether an openid-connect identity provider
// enforces a token audience (audience_required is set and non-empty).
func oidcRequiresAudience(idp *aigw.IdentityProvider) bool {
	return idp != nil && len(configStrings(idp.Config, "audience_required")) > 0
}

// applyOIDCFieldsToOAuth2 lowers an openid-connect identity provider's config
// fields onto an ai-mcp-oauth2 plugin config, following aimap's shared field
// table so the forward and reverse mappings cannot drift. Fields absent from
// the provider config (and empty scalars for the array-first kinds) are left
// off the plugin entirely.
func applyOIDCFieldsToOAuth2(cfg, oidc map[string]any) {
	for _, f := range aimap.OIDCToMCPOAuth2Fields {
		switch f.Kind {
		case aimap.OAuth2Passthrough:
			if v, ok := oidc[f.OIDCKey]; ok {
				cfg[f.OAuth2Key] = v
			}
		case aimap.OAuth2FirstOfArray:
			setIfNotEmpty(cfg, f.OAuth2Key, firstConfigString(oidc, f.OIDCKey))
		case aimap.OAuth2FirstOfPaths:
			if p := firstConfigPath(oidc, f.OIDCKey); len(p) > 0 {
				cfg[f.OAuth2Key] = p
			}
		}
	}
}

// firstConfigString reads config[key] as a string, taking the first element
// when the value is an array (OIDC client_id/client_secret are arrays in the
// AI Gateway model but single strings in the ai-mcp-oauth2 plugin).
func firstConfigString(config map[string]any, key string) string {
	switch v := config[key].(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			s, _ := v[0].(string)
			return s
		}
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// firstConfigPath reads config[key] as an array-of-paths ([][]string) and
// returns its first path as a []any (the openid-connect consumer_claims shape
// lowered onto the ai-mcp-oauth2 single-path consumer_claim). It tolerates
// []any or []string path elements.
func firstConfigPath(config map[string]any, key string) []any {
	arr, ok := config[key].([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	switch p := arr[0].(type) {
	case []any:
		return p
	case []string:
		out := make([]any, len(p))
		for i, s := range p {
			out[i] = s
		}
		return out
	}
	return nil
}

// configStrings reads config[key] as a []string, accepting []any or []string.
func configStrings(config map[string]any, key string) []string {
	switch v := config[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
