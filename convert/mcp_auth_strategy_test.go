package convert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

// keyAuthWithMetadata references a key-auth auth strategy together with
// access.metadata, which is unsupported (OAuth Protected Resource Metadata
// cannot be served by key-auth).
const keyAuthWithMetadata = `
auth_strategies:
  - name: mcp-key-auth
    type: key-auth
    config:
      key_names: [apikey]
mcp_servers:
  - type: listener
    name: keyed-mcp
    config:
      route:
        paths: [/mcp/keyed]
    access:
      auth_strategies: [mcp-key-auth]
      metadata:
        endpoint: /mcp/keyed/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/keyed
        authorization_servers: [https://issuer.example.com]
    tools:
      - name: ping
        description: Ping
        method: GET
        path: /ping
        scheme: https
        host: keyed.internal
`

func TestMCPKeyAuthWithMetadataIsHardError(t *testing.T) {
	result, err := Convert([]byte(keyAuthWithMetadata), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindMCPServer, "keyed-mcp",
		"access.metadata", "access.auth_strategies[0]")
	require.Contains(t, issue.Reason, "OAuth Protected Resource Metadata is not supported for key-auth")
	require.Empty(t, result.Output.Services, "the MCP server is left out")
}

// oidcOnUnsupportedListener puts auth strategy and metadata access on an
// upstream-server MCP server, which does not support it.
const oidcOnUnsupportedListener = `
auth_strategies:
  - name: okta-oidc
    type: openid-connect
    config:
      issuer: https://issuer.example.com
mcp_servers:
  - type: upstream-server
    name: upstream-mcp
    upstream_url: https://upstream.example.com/mcp
    config:
      route:
        paths: [/mcp/upstream]
      tools_cache_ttl_seconds: 300
    access:
      auth_strategies: [okta-oidc]
      metadata:
        endpoint: /mcp/upstream/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/upstream
        authorization_servers: [https://issuer.example.com]
    tools:
      - name: upstreamTool
        description: An upstream tool
`

func TestMCPAuthStrategyUnsupportedListenerIsHardError(t *testing.T) {
	result, err := Convert([]byte(oidcOnUnsupportedListener), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindMCPServer, "upstream-mcp",
		"access.auth_strategies", "access.metadata")
	require.Contains(t, issue.Reason, "only supported for listener, conversion-listener, and passthrough-listener")
}

// TestMCPOIDCWithoutIssuerIsHardError checks that an openid-connect strategy
// without an issuer leaves out the MCP server, with and without metadata.
func TestMCPOIDCWithoutIssuerIsHardError(t *testing.T) {
	const metadata = `
      metadata:
        resource: https://api.example.com/mcp
        authorization_servers: [https://issuer.example.com]`
	for name, access := range map[string]string{"with metadata": metadata, "without metadata": ""} {
		t.Run(name, func(t *testing.T) {
			result, err := Convert([]byte(`
auth_strategies:
  - name: no-issuer
    type: openid-connect
    config: {client_id: [mcp-client], client_secret: [secret]}
mcp_servers:
  - type: listener
    name: no-issuer-mcp
    config:
      route: {paths: [/mcp/no-issuer]}
    access:
      auth_strategies: [no-issuer]`+access+`
    tools:
      - {name: ping, description: Ping, method: GET, path: /ping, scheme: https, host: ping.internal}
`), testOptions)
			require.NoError(t, err)
			issue := requireIssue(t, result.Issues, Error, MissingRequiredConfigField,
				kindMCPServer, "no-issuer-mcp", "access.auth_strategies[0]")
			require.Contains(t, issue.Reason, "config.issuer")
			require.Empty(t, result.Output.Services, "the MCP server is left out")
		})
	}
}

// oidcMetadataWithoutAuthServers omits metadata.authorization_servers so the
// OIDC issuer is used as the fallback.
const oidcMetadataWithoutAuthServers = `
auth_strategies:
  - name: okta-oidc
    type: openid-connect
    config:
      issuer: https://issuer.example.com/oauth2
mcp_servers:
  - type: listener
    name: fallback-mcp
    config:
      route:
        paths: [/mcp/fallback]
    access:
      auth_strategies: [okta-oidc]
      metadata:
        endpoint: /mcp/fallback/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/fallback
    tools:
      - name: ping
        description: Ping
        method: GET
        path: /ping
        scheme: https
        host: fallback.internal
`

func TestMCPOIDCFallsBackToIssuerForAuthServers(t *testing.T) {
	out, warnings, err := convertYAML([]byte(oidcMetadataWithoutAuthServers))
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "name: openid-connect")
	require.NotContains(t, s, "ai-mcp-oauth2")
	require.Contains(t, s, "authorization_servers")
	require.Contains(t, s, "https://issuer.example.com/oauth2",
		"issuer should populate authorization_servers when metadata omits them")
}

// mcpTokenExchangeInput is an openid-connect strategy with token_exchange on
// an MCP server with access.metadata.
const mcpTokenExchangeInput = `
auth_strategies:
  - display_name: Okta OIDC
    name: okta-oidc-te
    type: openid-connect
    config:
      issuer: https://dev-123456.okta.com/oauth2/default
      client_id: [mcp-client]
      client_secret: ["{vault://ai-vault/okta-client-secret}"]
      cache_tokens_salt: pepper
      token_exchange:
        subject_token_issuers:
          - issuer: https://dev-123456.okta.com/oauth2/default
            conditions:
              has_audience: [mcp-api]
            verify_signature: true
        request:
          audience: [mcp-api]
        cache:
          enabled: true
          ttl: 3600
mcp_servers:
  - type: conversion-listener
    display_name: MCP with metadata
    name: mcp-with-metadata
    config:
      route:
        paths: [/mcp/with-meta]
    access:
      auth_strategies: [okta-oidc-te]
      metadata:
        endpoint: /mcp/with-meta/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/with-meta
        authorization_servers:
          - https://dev-123456.okta.com/oauth2/default
    tools:
      - name: echo
        description: echo
        method: GET
        path: /anything
        scheme: https
        host: httpbin.internal
`

// TestMCPMetadataKeepsTokenExchange checks that the openid-connect plugin
// keeps token_exchange.
func TestMCPMetadataKeepsTokenExchange(t *testing.T) {
	result, err := Convert([]byte(mcpTokenExchangeInput), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	s, err := result.Output.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(s), "name: openid-connect")
	require.Contains(t, string(s), "token_exchange:")
	require.NotContains(t, string(s), "ai-mcp-oauth2")
}

// TestMCPOIDCMetadataBelowGate checks that an openid-connect strategy with
// metadata leaves out the MCP server below 2.3.
func TestMCPOIDCMetadataBelowGate(t *testing.T) {
	opts := testOptions
	opts.MinRuntimeVersion = "2.2"
	result, err := Convert([]byte(mcpTokenExchangeInput), opts)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, UnsupportedInMinRuntimeVersion,
		kindMCPServer, "mcp-with-metadata", "access.metadata", "access.auth_strategies[0]")
	require.Contains(t, issue.Reason, `requires minimum runtime version "2.3"`)
	require.Empty(t, result.Output.Services, "the MCP server is left out")
}

// tokenVaultMCPWithMetadata is an upstream Token Vault MCP server with
// access.metadata. The auth plugin must keep the caller's bearer token, or
// the Token Vault exchange has no subject token.
const tokenVaultMCPWithMetadata = `
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
auth_strategies:
  - name: okta-oidc
    type: openid-connect
    config:
      issuer: https://issuer.example.com/oauth2
mcp_servers:
  - type: passthrough-listener
    name: vaulted-mcp
    upstream_url: https://tools.example.com
    config:
      route:
        paths: [/mcp/vaulted]
    datastores: [{name: tv}]
    token_vault:
      provider: github
      credential:
        outbound:
          location: header
          name: x-github-token
          prefix: ""
    access:
      auth_strategies: [okta-oidc]
      metadata:
        endpoint: /mcp/vaulted/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/vaulted
        authorization_servers: [https://issuer.example.com/oauth2]
    tools:
      - name: ping
        description: Ping
        method: GET
        path: /ping
        scheme: https
        host: tools.example.com
`

// tokenVaultMCPMetadataOnly is tokenVaultMCPWithMetadata without the auth
// strategy. Only this shape lowers to ai-mcp-oauth2.
var tokenVaultMCPMetadataOnly = func() string {
	const strategyRef = "      auth_strategies: [okta-oidc]\n"
	const strategyBlock = `auth_strategies:
  - name: okta-oidc
    type: openid-connect
    config:
      issuer: https://issuer.example.com/oauth2
`
	s := strings.Replace(tokenVaultMCPWithMetadata, "    access:\n"+strategyRef, "    access:\n", 1)
	return strings.Replace(s, strategyBlock, "", 1)
}()

func TestMCPOAuth2PassthroughForTokenVault(t *testing.T) {
	// A metadata-only vault server keeps ai-mcp-oauth2. The vault needs
	// passthrough_credentials.
	out, warnings, err := convertYAML([]byte(tokenVaultMCPMetadataOnly))
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "passthrough_credentials: true")
	require.Contains(t, s, "name: ai-mcp-oauth2")
}

// TestMCPOIDCTokenVaultForcesHideCredentials checks that the openid-connect
// plugin gets hide_credentials false with token_vault. The Token Vault needs
// the caller's bearer token.
func TestMCPOIDCTokenVaultForcesHideCredentials(t *testing.T) {
	src := strings.Replace(tokenVaultMCPWithMetadata,
		"      issuer: https://issuer.example.com/oauth2\n",
		"      issuer: https://issuer.example.com/oauth2\n      hide_credentials: true\n", 1)
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Warning, ConfigFieldOverridden,
		kindMCPServer, "vaulted-mcp", "access.auth_strategies[0]")
	require.Contains(t, issue.Reason, "hide_credentials will be overridden to false")
	s, err := result.Output.ToYAML()
	require.NoError(t, err)
	out := string(s)
	require.Contains(t, out, "name: openid-connect")
	require.Contains(t, out, "hide_credentials: false")
	require.Contains(t, out, "protected_resource_metadata:")
	require.NotContains(t, out, "ai-mcp-oauth2")
}

// TestMCPKeyAuthKeepsHideCredentials checks the KOKO-4840 fix. A key-auth
// strategy with hide_credentials true keeps the key away from the upstream.
// A conversion-listener needs the key on the tool call back through its route.
// A Token Vault reads the key from the request.
func TestMCPKeyAuthKeepsHideCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, mcpType, server, extra string
		wantHide                     bool
	}{
		{
			name: "passthrough forward_client_headers on", mcpType: "passthrough-listener",
			server: "server: {forward_client_headers: true}", wantHide: true,
		},
		{
			name: "passthrough forward_client_headers off", mcpType: "passthrough-listener",
			server: "server: {forward_client_headers: false}", wantHide: true,
		},
		{
			name: "conversion-listener", mcpType: "conversion-listener",
			server: "server: {timeout: 10000}", wantHide: false,
		},
		{
			name: "passthrough with token_vault", mcpType: "passthrough-listener",
			server: "server: {timeout: 10000}", extra: "token_vault: {provider: github}", wantHide: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `
auth_strategies:
  - name: mcp-key-auth
    type: key-auth
    config:
      key_names: [apikey]
      hide_credentials: true
mcp_servers:
  - type: ` + tc.mcpType + `
    name: upstream-mcp
    upstream_url: https://mcp.internal.kongair.com/mcp
    config:
      route: {paths: [/mcp/upstream]}
      ` + tc.server + `
    access:
      auth_strategies: [mcp-key-auth]
    tools:
      - {name: list-reports, description: List reports, method: GET, path: /reports}
    ` + tc.extra + `
`
			out, warnings := convertMCP(t, src)

			var keyAuth *kong.Plugin
			for _, p := range routePlugins(t, out, "upstream-mcp") {
				if p.Name == "key-auth" {
					keyAuth = &p
				}
			}
			require.NotNil(t, keyAuth)
			require.Equal(t, tc.wantHide, keyAuth.Config["hide_credentials"])
			if tc.wantHide {
				require.Empty(t, warnings)
			} else {
				require.Contains(t, strings.Join(warnings, "\n"), "hide_credentials will be overridden to false")
			}
		})
	}
}
