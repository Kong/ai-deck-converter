package convert

import (
	"encoding/base64"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// mcpAuthStrategyPlugins builds the authentication plugin(s) for an MCP server's
// access.auth_strategies / access.metadata, appending the metadata endpoint
// path to the route when an ai-mcp-oauth2 plugin is produced. It returns nil
// when the server declares no auth strategy or metadata access.
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

	// Listener guard: only listener-style modes can front the ai-mcp-oauth2
	// plugin (and its metadata endpoint). Reject rather than drop the requested
	// authentication behavior.
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

	idp, idpField, err := c.mcpAccessAuthStrategy(src, m)
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
		return nil, c.fail(src.at("access.metadata", idpField), ConfigFieldInvalid,
			"MCP server %q references key-auth auth strategy %q with access.metadata; "+
				"OAuth Protected Resource Metadata is not supported for key-auth",
			m.Name, idp.Name)
	}

	var plugins []kong.Plugin
	if meta != nil {
		// From 2.3 the openid-connect plugin itself serves the Protected
		// Resource Metadata (protected_resource_metadata block) and resolves
		// Kong Identity principals, so an openid-connect strategy + metadata
		// lowers to that plugin with the strategy config carried verbatim —
		// the principals block in particular, which ai-mcp-oauth2 cannot
		// represent. Metadata without a strategy stays on ai-mcp-oauth2 (it
		// serves discovery there), so every server with configured metadata
		// keeps serving discovery.
		build, source := c.mcpOAuth2Plugin, mcpOAuth2Source
		if idp != nil && idp.Type == authStrategyTypeOpenIDConnect && c.supports(aimap.FeatureMCPIdentityAuth) {
			build, source = c.mcpOIDCPlugin, mcpOIDCSource
		}
		plugin, err := build(src, m, idp, meta)
		if err != nil {
			return nil, err
		}
		if meta.Endpoint != "" {
			route.Paths = append(route.Paths, meta.Endpoint)
		}
		plugin.Source = source(m, idp, meta)
		plugins = []kong.Plugin{plugin}
	} else {
		// No metadata: emit a plain passthrough auth plugin (key-auth or
		// openid-connect) named after the auth strategy type.
		if idp == nil {
			return nil, nil
		}
		cfg := make(map[string]any, len(idp.Config))
		maps.Copy(cfg, idp.Config)
		if idp.Type == "key-auth" {
			// An MCP listener's credential must survive the plugin: ai-mcp-proxy
			// reads the authenticated request itself and carries it onto the
			// internal request it issues when executing a tool. Forced rather
			// than defaulted, so a strategy that
			// sets hide_credentials: true cannot silently strip it here -- the
			// strategy may be shared with models/agents, where hiding it is fine.
			if hide, _ := idp.Config["hide_credentials"].(bool); hide {
				c.warn(src.at(idpField), ConfigFieldOverridden,
					"MCP server %q references key-auth auth strategy %q with hide_credentials: true. "+
						"This will be ignored, and hide_credentials will be overridden to false "+
						"because ai-mcp-proxy needs the credential to execute tools.",
					m.Name, idp.Name)
			}
			cfg["hide_credentials"] = false
			c.deriveTokenVaultInbound(src, m, idp, route)
		}
		plugins = []kong.Plugin{{Name: idp.Type, Config: cfg, Source: source("identity_provider", idp.Name, "config")}}
	}

	// ai-mcp-proxy's oauth_access_token ACL subjects read the token claims
	// ai-mcp-oauth2 publishes (kong.ctx.shared.ai_mcp_oauth2); no other auth
	// plugin sets that context, so the combination cannot work. The check
	// lives here, where the whole auth-plugin decision is visible, not inside
	// one builder. An oauth_access_token ACL without any auth plugin is left
	// alone (nothing to bind the subjects to; see case 08's golden).
	if m.Access.ACLAttributeType == "oauth_access_token" && plugins[0].Name != "ai-mcp-oauth2" {
		return nil, c.fail(src.at("access.acl_attribute_type"), ConfigFieldInvalid,
			"MCP server %q sets acl_attribute_type oauth_access_token, but the route's auth plugin is %q. "+
				"oauth_access_token ACLs require the ai-mcp-oauth2 plugin: pair access.metadata with an "+
				"openid-connect strategy at min_runtime_version below 2.3. Otherwise use a consumer "+
				"acl_attribute_type.", m.Name, plugins[0].Name)
	}
	return plugins, nil
}

// deriveTokenVaultInbound fills the ai-mcp-proxy token_vault inbound
// credential from the key-auth strategy when the MCP server sets none.
// The plugin default inbound Authorization Bearer does not match where
// key-auth expects the credential. An explicit inbound is never touched.
func (c *Converter) deriveTokenVaultInbound(
	src ConversionIssueSource, m *aigw.MCPServer, idp *aigw.AuthStrategy, route *kong.Route,
) {
	tv := m.TokenVault
	if tv == nil || (tv.Credential != nil && tv.Credential.Inbound != nil) {
		return
	}
	loc := aimap.TokenVaultInboundFromKeyAuth(idp.Config)
	if loc == nil {
		c.warn(src.at("token_vault.credential.inbound"), BehaviorMayDiffer,
			"MCP server %q: the key-auth strategy does not read its key from the headers, so "+
				"token_vault.credential.inbound cannot be derived from it. The data plane keeps the "+
				"default inbound (Authorization: Bearer), which the exchange will not find.", m.Name)
		return
	}
	pi := slices.IndexFunc(route.Plugins, func(p kong.Plugin) bool { return p.Name == "ai-mcp-proxy" })
	if pi < 0 {
		return
	}
	auth, _ := route.Plugins[pi].Config["auth"].(map[string]any)
	block, _ := auth["token_vault"].(map[string]any)
	cred, _ := block["credential"].(map[string]any)
	if cred == nil {
		cred = map[string]any{}
		block["credential"] = cred
	}
	cred["inbound"] = aimap.TokenVaultLocationToPlugin(loc)
	c.warn(src.at("token_vault.credential.inbound"), ConfigFieldOverridden,
		"MCP server %q: token_vault.credential.inbound is derived from the route's key-auth plugin "+
			"(header %q, the strategy's first key name, no prefix), because the plugin default "+
			"Authorization: Bearer does not match where key-auth reads the key.", m.Name, loc.Name)
}

func mcpOAuth2Source(
	m *aigw.MCPServer,
	idp *aigw.AuthStrategy,
	meta *aigw.MCPProtectedResourceMetadata,
) *kong.Source {
	return source("mcp_server", m.Name, "access.metadata",
		kong.FieldMapping{GeneratedPrefix: "config.resource", SourcePrefix: "access.metadata.resource"},
		kong.FieldMapping{
			GeneratedPrefix: "config.authorization_servers",
			SourcePrefix:    authorizationServersSource(idp, meta),
		},
		kong.FieldMapping{
			GeneratedPrefix: "config.scopes_supported",
			SourcePrefix:    scopesSupportedSource(idp, meta),
		},
		kong.FieldMapping{GeneratedPrefix: "config.metadata_endpoint", SourcePrefix: "access.metadata.endpoint"},
		kong.FieldMapping{
			GeneratedPrefix: "config.metadata_discovery_endpoint",
			SourcePrefix:    "access.metadata.discovery_endpoint",
		},
		kong.FieldMapping{GeneratedPrefix: "config.client_id", SourcePrefix: "access.auth_strategies"},
		kong.FieldMapping{GeneratedPrefix: "config.client_secret", SourcePrefix: "access.auth_strategies"},
		kong.FieldMapping{GeneratedPrefix: "config.ssl_verify", SourcePrefix: "access.auth_strategies"},
	)
}

// mcpAccessAuthStrategy resolves the single (schema max 1) auth strategy
// referenced by an MCP server's access block and returns its field path. An
// unknown reference fails the server.
func (c *Converter) mcpAccessAuthStrategy(
	src ConversionIssueSource, m *aigw.MCPServer,
) (*aigw.AuthStrategy, string, error) {
	if len(m.Access.AuthStrategies) == 0 {
		return nil, "", nil
	}
	const field = "access.auth_strategies[0]"
	ref := m.Access.AuthStrategies[0]
	idp := c.authStrategies[ref]
	if idp == nil {
		return nil, "", c.fail(src.at(field), UndefinedReference,
			"MCP server %q references unknown auth strategy %q", m.Name, ref)
	}
	if err := c.checkAuthStrategyType(src, field, idp); err != nil {
		return nil, "", err
	}
	return idp, field, nil
}

// mcpProtectedResource derives the fields both MCP auth plugins require from
// an MCP server's Protected Resource Metadata: the resource URI and the
// authorization servers (with the OIDC issuer as the fallback). Both are
// required; a missing one fails the server.
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

// mcpEffectiveAuthServers resolves the authorization servers an MCP auth
// plugin emits: the metadata's list, falling back to the OIDC issuer. The
// Source helpers derive their labels from the same resolution, so the emitted
// value and its attribution cannot drift.
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

// mcpEffectiveScopes resolves the scopes_supported an MCP auth plugin emits:
// the metadata's list, falling back to the OIDC scopes.
func mcpEffectiveScopes(
	meta *aigw.MCPProtectedResourceMetadata, idp *aigw.AuthStrategy,
) []string {
	if len(meta.ScopesSupported) == 0 && idp != nil {
		return configStrings(idp.Config, "scopes")
	}
	return meta.ScopesSupported
}

// mcpOAuth2Plugin builds an ai-mcp-oauth2 plugin from an MCP server's Protected
// Resource Metadata and (optional) openid-connect auth strategy. Required
// plugin fields are resource and authorization_servers; the metadata is the
// primary source, with the OIDC issuer/scopes filling in when the metadata
// omits them.
func (c *Converter) mcpOAuth2Plugin(
	src ConversionIssueSource, m *aigw.MCPServer, idp *aigw.AuthStrategy, meta *aigw.MCPProtectedResourceMetadata,
) (kong.Plugin, error) {
	cfg := map[string]any{}

	resource, authServers, err := c.mcpProtectedResource(src, m, idp, meta)
	if err != nil {
		return kong.Plugin{}, err
	}
	cfg["resource"] = resource
	cfg["authorization_servers"] = authServers

	if scopes := mcpEffectiveScopes(meta, idp); len(scopes) > 0 {
		cfg["scopes_supported"] = scopes
	}

	setIfNotEmpty(cfg, "metadata_endpoint", meta.Endpoint)
	setIfNotEmpty(cfg, "metadata_discovery_endpoint", meta.DiscoveryEndpoint)
	if idp != nil {
		// token_exchange has no representation in ai-mcp-oauth2: the two
		// schemas differ and ai-mcp-oauth2 requires a token_endpoint the
		// openid-connect strategy does not carry (see
		// internal/aimap/mcp_oauth2.go, where token_exchange is deliberately
		// excluded from OIDCToMCPOAuth2Fields). Dropping it would silently
		// remove a security control (the caller's token forwarded verbatim,
		// no exchange), so fail rather than emit a plugin that quietly omits
		// it. The openid-connect lowering carries token_exchange verbatim —
		// it is a real openid-connect plugin field.
		if te, ok := idp.Config["token_exchange"]; ok && !isEmptyConfigValue(te) {
			return kong.Plugin{}, c.fail(src.at("access.auth_strategies[0]"), ConfigFieldInvalid,
				"MCP server %q auth strategy %q sets config.token_exchange, which ai-mcp-oauth2 "+
					"cannot represent; the token exchange would NOT be applied and the caller's token "+
					"forwarded verbatim. Remove access.metadata to keep an openid-connect plugin, "+
					"or drop token_exchange from the strategy.",
				m.Name, idp.Name)
		}
		applyOIDCFieldsToOAuth2(cfg, idp.Config)
		// The gateway rejects client_id without a client credential.
		// client_secret is the only credential that maps onto the plugin.
		// A client_id without a secret is left out.
		if firstConfigString(idp.Config, "client_id") != "" &&
			firstConfigString(idp.Config, "client_secret") == "" {
			delete(cfg, "client_id")
			c.warn(src.at("access.auth_strategies[0]"), ConfigFieldIgnored,
				"MCP server %q auth strategy %q sets client_id but no client_secret. "+
					"The gateway requires a client credential with client_id, so the plugin "+
					"leaves client_id out. Token validation does not use it.",
				m.Name, idp.Name)
		}
		proxyConfig, err := oidcProxyConfig(idp.Config)
		if err != nil {
			return kong.Plugin{}, c.fail(src.at("access.auth_strategies[0]"), ConfigFieldInvalid,
				"MCP server %q references an auth strategy with unsupported proxy configuration: %s",
				m.Name, err)
		}
		if proxyConfig != nil {
			cfg["proxy_config"] = proxyConfig
		}
		// passthrough_credentials is the inverse of the provider's
		// hide_credentials (OIDC default true); only the non-default
		// (hide_credentials: false) is carried, as passthrough_credentials: true.
		if hide, ok := idp.Config["hide_credentials"].(bool); ok && !hide {
			cfg["passthrough_credentials"] = true
		}
	}

	// The Token Vault auth provider exchanges the caller's bearer token for
	// the upstream credential. ai-mcp-oauth2 clears the Authorization header
	// by default, and the exchange then fails. passthrough_credentials keeps
	// the token; the Token Vault auth provider removes it before proxying.
	// This path only serves metadata-only servers now (a strategy attached at
	// 2.3 lowers to the openid-connect plugin, and token vault itself is gated
	// at 2.3), so there is no strategy hide_credentials to warn about here.
	if m.TokenVault != nil {
		cfg["passthrough_credentials"] = true
	}

	// insecure_relaxed_audience_validation mirrors the provider's audience
	// enforcement: AI Gateway validates the audience only when audience_required
	// is set, so the plugin relaxes validation (true) unless the provider
	// required one. Always emitted (see aimap.OIDCToMCPOAuth2Fields doc).
	cfg["insecure_relaxed_audience_validation"] = !oidcRequiresAudience(idp)

	return kong.Plugin{Name: "ai-mcp-oauth2", Config: cfg}, nil
}

// mcpOIDCPlugin builds an openid-connect plugin for an MCP server from its
// Protected Resource Metadata and an openid-connect auth strategy, for
// dataplanes from 2.3 (FeatureMCPIdentityAuth). The strategy config is copied
// verbatim — the principals block in particular, which ai-mcp-oauth2 cannot
// represent — and the metadata becomes the plugin's protected_resource_metadata
// block, which the plugin itself serves as RFC 9728 discovery.
//
// Deliberately unlike mcpOAuth2Plugin: no insecure_relaxed_audience_validation
// synthesis (openid-connect validates the audience only when audience_required
// is set, the same effective behavior), no client_id-without-secret drop (a
// public client is valid), no proxy_config lowering (openid-connect keeps the
// flat proxy fields), and auth_methods is forced to bearer — ai-mcp-oauth2 is
// bearer-only (it has no auth_methods field and the strategy's list is dropped
// by the ai-mcp-oauth2 field map), so interactive flows must not become live
// on an MCP route.
func (c *Converter) mcpOIDCPlugin(
	src ConversionIssueSource, m *aigw.MCPServer, idp *aigw.AuthStrategy, meta *aigw.MCPProtectedResourceMetadata,
) (kong.Plugin, error) {
	resource, authServers, err := c.mcpProtectedResource(src, m, idp, meta)
	if err != nil {
		return kong.Plugin{}, err
	}

	cfg := make(map[string]any, len(idp.Config))
	maps.Copy(cfg, idp.Config)

	// Bearer-only on an MCP route, matching ai-mcp-oauth2. The strategy's
	// auth_methods list is dropped by the ai-mcp-oauth2 field map today, so
	// interactive flows were never live here; warn when the strategy asked for
	// more than bearer so the narrowing is visible.
	if methods := configStrings(idp.Config, "auth_methods"); len(methods) > 1 ||
		(len(methods) == 1 && methods[0] != "bearer") {
		c.warn(src.at("access.auth_strategies[0]"), ConfigFieldOverridden,
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
		// The 2.3 openid-connect protected_resource_metadata block has no
		// discovery-endpoint override (unlike ai-mcp-oauth2).
		c.warn(src.at("access.metadata.discovery_endpoint"), ConfigFieldIgnored,
			"MCP server %q access.metadata sets discovery_endpoint, which the openid-connect plugin's "+
				"protected_resource_metadata block does not support; it will be ignored.",
			m.Name)
	}

	// The Token Vault auth provider exchanges the caller's bearer token for
	// the upstream credential. openid-connect strips the Authorization header
	// when hide_credentials is true (its default is false, but strategies
	// commonly set true), and the exchange then fails.
	if m.TokenVault != nil {
		if hide, _ := idp.Config["hide_credentials"].(bool); hide {
			c.warn(src.at("access.auth_strategies[0]"), ConfigFieldOverridden,
				"MCP server %q references openid-connect auth strategy %q with hide_credentials: true. "+
					"This will be ignored, and hide_credentials will be overridden to false "+
					"because the Token Vault needs the caller's bearer token to resolve the upstream credential.",
				m.Name, idp.Name)
		}
		cfg["hide_credentials"] = false
	}

	return kong.Plugin{Name: "openid-connect", Config: cfg}, nil
}

// mcpOIDCSource describes where an mcpOIDCPlugin's fields came from, mirroring
// mcpOAuth2Source with the metadata fields nested under the
// protected_resource_metadata block.
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

// oidcProxyConfig lowers openid-connect's legacy flat proxy fields into the
// proxy_config record used by ai-mcp-oauth2. The target has one proxy scheme
// and one Basic-auth credential pair shared by both HTTP and HTTPS proxies, so
// configurations that require distinct schemes or credentials are rejected
// rather than silently changed.
func oidcProxyConfig(oidc map[string]any) (map[string]any, error) {
	httpProxy, err := oidcProxyEndpoint(configString(oidc, "http_proxy"))
	if err != nil {
		return nil, fmt.Errorf("http_proxy: %w", err)
	}
	httpsProxy, err := oidcProxyEndpoint(configString(oidc, "https_proxy"))
	if err != nil {
		return nil, fmt.Errorf("https_proxy: %w", err)
	}

	httpAuth := configString(oidc, "http_proxy_authorization")
	httpsAuth := configString(oidc, "https_proxy_authorization")
	if httpAuth != "" && httpProxy == nil {
		return nil, fmt.Errorf("http_proxy_authorization requires http_proxy")
	}
	if httpsAuth != "" && httpsProxy == nil {
		return nil, fmt.Errorf("https_proxy_authorization requires https_proxy")
	}
	if httpProxy != nil && httpsProxy != nil {
		if httpProxy.scheme != httpsProxy.scheme {
			return nil, fmt.Errorf("http_proxy and https_proxy must use the same scheme")
		}
		if (httpAuth == "") != (httpsAuth == "") || (httpAuth != "" && httpAuth != httpsAuth) {
			return nil, fmt.Errorf("http_proxy and https_proxy must use the same authorization")
		}
	}

	if httpProxy == nil && httpsProxy == nil && httpAuth == "" &&
		httpsAuth == "" && configString(oidc, "no_proxy") == "" {
		return nil, nil
	}

	proxyConfig := map[string]any{}
	if httpProxy != nil {
		proxyConfig["http_proxy_host"] = httpProxy.host
		if httpProxy.port != nil {
			proxyConfig["http_proxy_port"] = *httpProxy.port
		}
		proxyConfig["proxy_scheme"] = httpProxy.scheme
	}
	if httpsProxy != nil {
		proxyConfig["https_proxy_host"] = httpsProxy.host
		if httpsProxy.port != nil {
			proxyConfig["https_proxy_port"] = *httpsProxy.port
		}
		if _, exists := proxyConfig["proxy_scheme"]; !exists {
			proxyConfig["proxy_scheme"] = httpsProxy.scheme
		}
	}
	setIfNotEmpty(proxyConfig, "no_proxy", configString(oidc, "no_proxy"))

	authorization := httpAuth
	if authorization == "" {
		authorization = httpsAuth
	}
	if authorization != "" {
		username, password, err := basicProxyCredentials(authorization)
		if err != nil {
			return nil, err
		}
		proxyConfig["auth_username"] = username
		proxyConfig["auth_password"] = password
	}

	return proxyConfig, nil
}

type oidcProxyEndpointConfig struct {
	host   string
	port   *int
	scheme string
}

func oidcProxyEndpoint(raw string) (*oidcProxyEndpointConfig, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("must be an absolute URL")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("must not include user info, a path, a query, or a fragment")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("scheme must be http or https")
	}

	endpoint := &oidcProxyEndpointConfig{host: u.Hostname(), scheme: u.Scheme}
	if rawPort := u.Port(); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("has an invalid port")
		}
		endpoint.port = &port
	}
	return endpoint, nil
}

func basicProxyCredentials(authorization string) (string, string, error) {
	parts := strings.Fields(authorization)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return "", "", fmt.Errorf("proxy authorization must use the Basic scheme")
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return "", "", fmt.Errorf("proxy authorization has invalid Basic credentials")
	}
	username, password, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", fmt.Errorf("proxy authorization Basic credentials must contain a username and password")
	}
	return username, password, nil
}

func configString(config map[string]any, key string) string {
	v, _ := config[key].(string)
	return v
}

// oidcRequiresAudience reports whether an openid-connect auth strategy
// enforces a token audience (audience_required is set and non-empty).
func oidcRequiresAudience(idp *aigw.AuthStrategy) bool {
	return idp != nil && len(configStrings(idp.Config, "audience_required")) > 0
}

// isEmptyConfigValue reports whether a decoded config value carries no data:
// nil, or an empty map/slice. Used to tell "token_exchange present" from an
// absent or blank block so the drop warning only fires when a control is real.
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

// applyOIDCFieldsToOAuth2 lowers an openid-connect auth strategy's config
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

	// ai-mcp-oauth2 has only the single-path credential_claim.
	// The first credential_claims path wins over credential_claim.
	// Revert gives back credential_claim only.
	if p := firstConfigPath(oidc, "credential_claims"); len(p) > 0 {
		cfg["credential_claim"] = p
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

// configStrings reads config[key] as a []string (see aimap.ConfigStrings).
func configStrings(config map[string]any, key string) []string {
	return aimap.ConfigStrings(config, key)
}
