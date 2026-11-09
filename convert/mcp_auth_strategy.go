package convert

import (
	"maps"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// mcpAuthStrategyPlugins builds the authentication plugin(s) for an MCP server's
// access.auth_strategies / access.metadata. It adds the metadata endpoint
// path to the route when the server has metadata. It returns nil when the
// server declares no auth strategy or metadata access.
//
// Unlike agents and models (convert/authstrategy.go), MCP auth never
// synthesizes an anonymous consumer / request-termination: the ai-mcp-oauth2
// plugin's consumer mapping is optional, and a bare key-auth plugin on the
// route enforces on its own.
func (c *Converter) mcpAuthStrategyPlugins(
	src ConversionIssueSource, m *aigw.MCPServer, route *kong.Route,
) ([]kong.Plugin, error) {
	meta := m.Access.Metadata
	if len(m.Access.AuthStrategies) == 0 && meta == nil {
		return nil, nil
	}

	// Only listener types accept an auth plugin and its metadata endpoint.
	if !aimap.MCPAccessAllowed(m.Type) {
		var fields []string
		if len(m.Access.AuthStrategies) > 0 {
			fields = append(fields, "access.auth_strategies")
		}
		if meta != nil {
			fields = append(fields, "access.metadata")
		}
		return nil, c.fail(src.at(fields...), ConfigFieldInvalid,
			"MCP server %q has type %q; access.auth_strategies/metadata are only "+
				"supported for listener, conversion-listener, and passthrough-listener",
			m.Name, m.Type)
	}

	idp, err := c.mcpAccessAuthStrategy(src, m)
	if err != nil {
		return nil, err
	}

	if idp != nil {
		if err := c.applyAuthStrategyTokenVault(idp); err != nil {
			return nil, err
		}
	}
	// key-auth cannot serve OAuth 2.0 Protected Resource Metadata; reject the
	// combination unconditionally.
	if meta != nil && idp != nil && idp.Type == "key-auth" {
		return nil, c.fail(src.at("access.metadata", mcpAuthStrategyField), ConfigFieldInvalid,
			"MCP server %q references key-auth auth strategy %q with access.metadata; "+
				"OAuth Protected Resource Metadata is not supported for key-auth",
			m.Name, idp.Name)
	}

	// Without metadata, idp is never nil. The function returns early when both
	// are missing.
	var plugin kong.Plugin
	if meta != nil {
		// Metadata without a strategy has no issuer for openid-connect.
		// It stays on ai-mcp-oauth2 for discovery.
		if idp != nil {
			if !c.supports(aimap.FeatureOIDCProtectedResourceMetadata) {
				return nil, c.failUnsupported(src.at("access.metadata", mcpAuthStrategyField),
					aimap.FeatureOIDCProtectedResourceMetadata,
					"access.metadata with an openid-connect auth strategy")
			}
			plugin, err = c.mcpOIDCPlugin(src, m, idp, meta)
		} else {
			plugin, err = c.mcpOAuth2Plugin(src, m, meta)
		}
		if err != nil {
			return nil, err
		}
		if meta.Endpoint != "" {
			route.Paths = append(route.Paths, meta.Endpoint)
		}
	} else {
		cfg := make(map[string]any, len(idp.Config))
		maps.Copy(cfg, idp.Config)
		if reason := c.mcpKeyAuthRevealReason(m, idp); reason != "" {
			c.revealMCPCredentials(src, m, idp, cfg, reason)
		}
		// The Token Vault reads the access token from the upstream header.
		if idp.Type == authStrategyTypeOpenIDConnect && m.TokenVault == nil {
			hideOIDCAccessToken(cfg)
		}
		plugin = kong.Plugin{Name: idp.Type, Config: cfg, Source: source("identity_provider", idp.Name, "config")}
	}

	// oauth_access_token ACL subjects read the claims in
	// kong.ctx.shared.ai_mcp_oauth2. ai-mcp-oauth2 always sets these claims.
	// openid-connect sets them from the release of protected_resource_metadata.
	oidcPublishesACLClaims := plugin.Name == authStrategyTypeOpenIDConnect &&
		c.supports(aimap.FeatureOIDCProtectedResourceMetadata)
	if m.Access.ACLAttributeType == "oauth_access_token" &&
		plugin.Name != "ai-mcp-oauth2" && !oidcPublishesACLClaims {
		c.warn(src.at("access.acl_attribute_type"), BehaviorMayDiffer,
			"MCP server %q sets acl_attribute_type oauth_access_token, but the route's auth plugin is %q. "+
				"oauth_access_token ACL subjects read the token claims published by ai-mcp-oauth2, "+
				"or by openid-connect from runtime 2.3. On older runtimes the ACL has no subjects to "+
				"match and denies every request.", m.Name, plugin.Name)
	}
	return []kong.Plugin{plugin}, nil
}

// mcpAuthStrategyField is the path of the auth strategy of an MCP server.
// The schema allows at most one.
const mcpAuthStrategyField = "access.auth_strategies[0]"

// mcpAccessAuthStrategy resolves the auth strategy referenced by an MCP
// server's access block. An unknown reference fails the server.
func (c *Converter) mcpAccessAuthStrategy(
	src ConversionIssueSource, m *aigw.MCPServer,
) (*aigw.AuthStrategy, error) {
	if len(m.Access.AuthStrategies) == 0 {
		return nil, nil
	}
	ref := m.Access.AuthStrategies[0]
	idp := c.authStrategies[ref]
	if idp == nil {
		return nil, c.fail(src.at(mcpAuthStrategyField), UndefinedReference,
			"MCP server %q references unknown auth strategy %q", m.Name, ref)
	}
	if err := c.checkAuthStrategyType(src, mcpAuthStrategyField, idp); err != nil {
		return nil, err
	}
	// The openid-connect plugin requires an issuer.
	if idp.Type == authStrategyTypeOpenIDConnect && firstConfigString(idp.Config, "issuer") == "" {
		return nil, c.fail(src.at(mcpAuthStrategyField), MissingRequiredConfigField,
			"MCP server %q references openid-connect auth strategy %q without config.issuer; "+
				"the openid-connect plugin requires one",
			m.Name, idp.Name)
	}
	return idp, nil
}

// mcpProtectedResource gets the resource and authorization servers of an MCP
// server. Both MCP auth plugins require them. A missing value fails the server.
func (c *Converter) mcpProtectedResource(
	src ConversionIssueSource, m *aigw.MCPServer, idp *aigw.AuthStrategy, meta *aigw.MCPProtectedResourceMetadata,
) (string, []string, error) {
	if meta.Resource == "" {
		return "", nil, c.fail(src.at("access.metadata.resource"), MissingRequiredConfigField,
			"MCP server %q access.metadata has no resource; MCP auth requires one", m.Name)
	}

	authServers := mcpEffectiveAuthServers(meta, idp)
	if len(authServers) == 0 {
		return "", nil, c.fail(src.at("access.metadata.authorization_servers"), MissingRequiredConfigField,
			"MCP server %q access.metadata has no authorization_servers and no issuer to fall back to; "+
				"MCP auth requires authorization_servers", m.Name)
	}
	return meta.Resource, authServers, nil
}

// mcpEffectiveAuthServers returns the metadata authorization servers, or else
// the OIDC issuer. The Source helpers use the same function. The value and its
// source always agree.
func mcpEffectiveAuthServers(
	meta *aigw.MCPProtectedResourceMetadata, idp *aigw.AuthStrategy,
) []string {
	if len(meta.AuthorizationServers) == 0 && idp != nil {
		if issuer := firstConfigString(idp.Config, "issuer"); issuer != "" {
			return []string{issuer}
		}
	}
	return meta.AuthorizationServers
}

// mcpEffectiveScopes returns the metadata scopes, or else the OIDC scopes.
func mcpEffectiveScopes(
	meta *aigw.MCPProtectedResourceMetadata, idp *aigw.AuthStrategy,
) []string {
	if len(meta.ScopesSupported) == 0 && idp != nil {
		return configStrings(idp.Config, "scopes")
	}
	return meta.ScopesSupported
}

// mcpOAuth2Plugin makes the ai-mcp-oauth2 plugin for an MCP server with
// metadata and no auth strategy.
func (c *Converter) mcpOAuth2Plugin(
	src ConversionIssueSource, m *aigw.MCPServer, meta *aigw.MCPProtectedResourceMetadata,
) (kong.Plugin, error) {
	resource, authServers, err := c.mcpProtectedResource(src, m, nil, meta)
	if err != nil {
		return kong.Plugin{}, err
	}
	cfg := map[string]any{
		"resource":              resource,
		"authorization_servers": authServers,
	}
	if len(meta.ScopesSupported) > 0 {
		cfg["scopes_supported"] = meta.ScopesSupported
	}
	setIfNotEmpty(cfg, "metadata_endpoint", meta.Endpoint)
	setIfNotEmpty(cfg, "metadata_discovery_endpoint", meta.DiscoveryEndpoint)

	// ai-mcp-oauth2 clears the Authorization header by default.
	// The Token Vault needs the caller's bearer token for its exchange.
	if m.TokenVault != nil {
		cfg["passthrough_credentials"] = true
	}
	// No strategy requires an audience.
	cfg["insecure_relaxed_audience_validation"] = true

	return kong.Plugin{
		Name:   "ai-mcp-oauth2",
		Config: cfg,
		Source: source("mcp_server", m.Name, "access.metadata",
			kong.FieldMapping{GeneratedPrefix: "config.resource", SourcePrefix: "access.metadata.resource"},
			kong.FieldMapping{
				GeneratedPrefix: "config.authorization_servers",
				SourcePrefix:    "access.metadata.authorization_servers",
			},
			kong.FieldMapping{
				GeneratedPrefix: "config.scopes_supported",
				SourcePrefix:    "access.metadata.scopes_supported",
			},
			kong.FieldMapping{GeneratedPrefix: "config.metadata_endpoint", SourcePrefix: "access.metadata.endpoint"},
			kong.FieldMapping{
				GeneratedPrefix: "config.metadata_discovery_endpoint",
				SourcePrefix:    "access.metadata.discovery_endpoint",
			},
		),
	}, nil
}

// mcpOIDCPlugin makes the openid-connect plugin for an MCP server with an
// auth strategy and metadata. auth_methods is always bearer.
func (c *Converter) mcpOIDCPlugin(
	src ConversionIssueSource, m *aigw.MCPServer, idp *aigw.AuthStrategy, meta *aigw.MCPProtectedResourceMetadata,
) (kong.Plugin, error) {
	resource, authServers, err := c.mcpProtectedResource(src, m, idp, meta)
	if err != nil {
		return kong.Plugin{}, err
	}

	cfg := make(map[string]any, len(idp.Config))
	maps.Copy(cfg, idp.Config)

	// Interactive flows would break the MCP authorization challenge.
	if methods := configStrings(idp.Config, "auth_methods"); len(methods) > 1 ||
		(len(methods) == 1 && methods[0] != "bearer") {
		c.warn(src.at(mcpAuthStrategyField), ConfigFieldOverridden,
			"MCP server %q auth strategy %q sets auth_methods beyond bearer. This will be overridden to "+
				"[bearer] because MCP routes validate bearer tokens only (matching ai-mcp-oauth2); "+
				"interactive flows would break the MCP authorization challenge.",
			m.Name, idp.Name)
	}
	cfg["auth_methods"] = []any{"bearer"}

	prm := map[string]any{
		"resource":              resource,
		"authorization_servers": authServers,
	}
	if scopes := mcpEffectiveScopes(meta, idp); len(scopes) > 0 {
		prm["scopes_supported"] = scopes
	}
	setIfNotEmpty(prm, "metadata_endpoint", meta.Endpoint)
	cfg["protected_resource_metadata"] = prm

	if meta.DiscoveryEndpoint != "" {
		// The openid-connect protected_resource_metadata block has no discovery
		// endpoint field.
		c.warn(src.at("access.metadata.discovery_endpoint"), ConfigFieldIgnored,
			"MCP server %q access.metadata sets discovery_endpoint, which the openid-connect plugin's "+
				"protected_resource_metadata block does not support; it will be ignored.",
			m.Name)
	}

	// The Token Vault exchanges the caller's bearer token for the upstream
	// credential. hide_credentials: true removes that token before the
	// exchange.
	if m.TokenVault != nil {
		c.revealMCPCredentials(src, m, idp, cfg,
			"the Token Vault needs the caller's bearer token to resolve the upstream credential")
	}
	hideOIDCAccessToken(cfg)

	return kong.Plugin{
		Name:   authStrategyTypeOpenIDConnect,
		Config: cfg,
		Source: mcpOIDCSource(m, idp, meta),
	}, nil
}

// mcpOIDCSource gives the source of each field of mcpOIDCPlugin.
func mcpOIDCSource(
	m *aigw.MCPServer,
	idp *aigw.AuthStrategy,
	meta *aigw.MCPProtectedResourceMetadata,
) *kong.Source {
	return source("mcp_server", m.Name, "access.metadata",
		kong.FieldMapping{
			GeneratedPrefix: "config.protected_resource_metadata.resource",
			SourcePrefix:    "access.metadata.resource",
		},
		kong.FieldMapping{
			GeneratedPrefix: "config.protected_resource_metadata.authorization_servers",
			SourcePrefix:    authorizationServersSource(idp, meta),
		},
		kong.FieldMapping{
			GeneratedPrefix: "config.protected_resource_metadata.scopes_supported",
			SourcePrefix:    scopesSupportedSource(idp, meta),
		},
		kong.FieldMapping{
			GeneratedPrefix: "config.protected_resource_metadata.metadata_endpoint",
			SourcePrefix:    "access.metadata.endpoint",
		},
		kong.FieldMapping{GeneratedPrefix: "config.client_id", SourcePrefix: "access.auth_strategies"},
		kong.FieldMapping{GeneratedPrefix: "config.client_secret", SourcePrefix: "access.auth_strategies"},
		kong.FieldMapping{GeneratedPrefix: "config.ssl_verify", SourcePrefix: "access.auth_strategies"},
	)
}

// authorizationServersSource reports whether the emitted authorization_servers
// came from the metadata or fell back to the OIDC issuer.
func authorizationServersSource(
	idp *aigw.AuthStrategy, meta *aigw.MCPProtectedResourceMetadata,
) string {
	if len(meta.AuthorizationServers) == 0 && len(mcpEffectiveAuthServers(meta, idp)) > 0 {
		return "access.auth_strategies"
	}
	return "access.metadata.authorization_servers"
}

// scopesSupportedSource reports whether the emitted scopes_supported came from
// the metadata or fell back to the OIDC scopes.
func scopesSupportedSource(
	idp *aigw.AuthStrategy, meta *aigw.MCPProtectedResourceMetadata,
) string {
	if len(meta.ScopesSupported) == 0 && len(mcpEffectiveScopes(meta, idp)) > 0 {
		return "access.auth_strategies"
	}
	return "access.metadata.scopes_supported"
}

// isEmptyConfigValue reports whether a decoded config value carries no data:
// nil, or an empty map/slice.
func isEmptyConfigValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	default:
		return false
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

// mcpKeyAuthRevealReason returns why the key-auth plugin of m must send the
// client's key on. It returns "" when the strategy's hide_credentials stays.
func (c *Converter) mcpKeyAuthRevealReason(m *aigw.MCPServer, idp *aigw.AuthStrategy) string {
	switch {
	case idp.Type != "key-auth":
		return ""
	case c.mcpReentersKeyAuthRoute(m):
		// A tool call comes back through this route. Its key-auth plugin
		// needs the client's key again.
		return "ai-mcp-proxy needs the credential to execute tools"
	case m.TokenVault != nil:
		return "the Token Vault reads the credential of the caller from the request"
	}
	return ""
}

// revealMCPCredentials sets hide_credentials to false in cfg. It warns when
// the strategy sets hide_credentials to true.
func (c *Converter) revealMCPCredentials(
	src ConversionIssueSource, m *aigw.MCPServer, idp *aigw.AuthStrategy, cfg map[string]any, reason string,
) {
	if hide, _ := idp.Config["hide_credentials"].(bool); hide {
		c.warn(src.at(mcpAuthStrategyField), ConfigFieldOverridden,
			"MCP server %q references %s auth strategy %q with hide_credentials: true. "+
				"This will be ignored, and hide_credentials will be overridden to false "+
				"because %s.",
			m.Name, idp.Type, idp.Name, reason)
	}
	cfg["hide_credentials"] = false
}
