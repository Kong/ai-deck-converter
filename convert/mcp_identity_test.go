package convert

import (
	"testing"

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
	// key-auth + metadata is a hard error regardless of strict mode.
	for _, strict := range []bool{false, true} {
		_, _, err := Convert([]byte(keyAuthWithMetadata), Options{Strict: strict})
		require.Error(t, err, "strict=%v", strict)
		require.Contains(t, err.Error(), "OAuth Protected Resource Metadata is not supported for key-auth")
		conversionErr, ok := AsConversionError(err)
		require.True(t, ok, "strict=%v", strict)
		require.Equal(t, "access.metadata", conversionErr.Diagnostics[0].Field)
	}
}

// oidcOnUnsupportedListener puts identity/metadata access on an
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

func TestMCPIdentityUnsupportedListenerIsHardError(t *testing.T) {
	_, _, err := Convert([]byte(oidcOnUnsupportedListener), Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "only supported for listener, conversion-listener, and passthrough-listener")

	conversionErr, ok := AsConversionError(err)
	require.True(t, ok)
	require.Len(t, conversionErr.Diagnostics, 2)
	require.Equal(t, "access.auth_strategies", conversionErr.Diagnostics[0].Field)
	require.Equal(t, "access.metadata", conversionErr.Diagnostics[1].Field)
	require.Equal(t, conversionErr.Diagnostics[0].Messages, conversionErr.Diagnostics[1].Messages)
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

func TestMCPOAuth2FallsBackToIssuerForAuthServers(t *testing.T) {
	out, warnings, err := Convert([]byte(oidcMetadataWithoutAuthServers), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "ai-mcp-oauth2")
	require.Contains(t, s, "authorization_servers")
	require.Contains(t, s, "https://issuer.example.com/oauth2",
		"issuer should populate authorization_servers when metadata omits them")
}

// oidcWithMappedFields carries a spread of the Gap 1 & 2 identity fields plus
// an OIDC-only field (cache_tokens_salt) that has no ai-mcp-oauth2 target.
const oidcWithMappedFields = `
auth_strategies:
  - name: okta-oidc
    type: openid-connect
    config:
      issuer: https://issuer.example.com
      client_auth: [client_secret_post, none]
      client_alg: [RS256]
      cache_introspection: true
      introspection_endpoint: https://issuer.example.com/introspect
      leeway: 30
      timeout: 5000
      http_proxy: http://proxy.example.com:8080
      http_proxy_authorization: Basic dGVzdDpwYXNz
      https_proxy: http://secure-proxy.example.com:8443
      https_proxy_authorization: Basic dGVzdDpwYXNz
      no_proxy: localhost,.example.com
      consumer_by: [username]
      consumer_claims: [[sub], [email]]
      upstream_headers:
        - header: X-User-ID
          path: [user, id]
      cache_tokens_salt: pepper
mcp_servers:
  - type: listener
    name: mapped-mcp
    config:
      route:
        paths: [/mcp/mapped]
    access:
      auth_strategies: [okta-oidc]
      metadata:
        endpoint: /mcp/mapped/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/mapped
        authorization_servers: [https://issuer.example.com]
    tools:
      - name: ping
        description: Ping
        method: GET
        path: /ping
        scheme: https
        host: mapped.internal
`

func TestMCPOAuth2MapsIdentityFields(t *testing.T) {
	out, warnings, err := Convert([]byte(oidcWithMappedFields), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	// Passthrough (verbatim) and rename.
	require.Contains(t, s, "cache_introspection: true")
	require.Contains(t, s, "introspection_endpoint: https://issuer.example.com/introspect")
	require.Contains(t, s, "timeout: 5000")
	require.Contains(t, s, "proxy_config:")
	require.Contains(t, s, "auth_username: test")
	require.Contains(t, s, "auth_password: pass")
	require.Contains(t, s, "http_proxy_host: proxy.example.com")
	require.Contains(t, s, "http_proxy_port: 8080")
	require.Contains(t, s, "https_proxy_host: secure-proxy.example.com")
	require.Contains(t, s, "https_proxy_port: 8443")
	require.Contains(t, s, "proxy_scheme: http")
	require.Contains(t, s, "no_proxy: localhost,.example.com")
	require.Contains(t, s, "upstream_headers:")
	require.Contains(t, s, "header: X-User-ID")
	require.Contains(t, s, "path:\n                    - user\n                    - id")
	require.Contains(t, s, "jwt_claims_leeway: 30", "leeway renames to jwt_claims_leeway")
	// FirstOfArray: OIDC array collapses to a single plugin scalar (element 0).
	require.Contains(t, s, "client_auth: client_secret_post")
	require.Contains(t, s, "client_alg: RS256")
	require.NotContains(t, s, "none", "only the first client_auth element is mapped")
	// FirstOfPaths: only the first consumer claim path survives.
	require.Contains(t, s, "consumer_claim:")
	require.NotContains(t, s, "email", "only the first consumer_claims path is mapped")
	// OIDC-only field with no target must be dropped.
	require.NotContains(t, s, "cache_tokens_salt")
	// No audience_required on this provider => relaxed validation by default.
	require.Contains(t, s, "insecure_relaxed_audience_validation: true")
}

func TestOIDCProxyConfigRejectsDistinctProxyAuthorization(t *testing.T) {
	_, err := oidcProxyConfig(map[string]any{
		"http_proxy":                "http://proxy.example.com:8080",
		"http_proxy_authorization":  "Basic dGVzdDpwYXNz",
		"https_proxy":               "http://secure-proxy.example.com:8443",
		"https_proxy_authorization": "Basic c2VjdXJlOnBhc3M=",
	})
	require.EqualError(t, err, "http_proxy and https_proxy must use the same authorization")
}

func TestBasicProxyCredentialsAcceptsUnpaddedBase64AndWhitespace(t *testing.T) {
	username, password, err := basicProxyCredentials("Basic\tdXNlcjpwdw")
	require.NoError(t, err)
	require.Equal(t, "user", username)
	require.Equal(t, "pw", password)
}

// oidcWithAudienceAndPassthrough exercises the two derived fields: a provider
// that disables hide_credentials and requires an audience.
const oidcWithAudienceAndPassthrough = `
auth_strategies:
  - name: okta-oidc
    type: openid-connect
    config:
      issuer: https://issuer.example.com
      hide_credentials: false
      audience_required: [https://api.example.com/mcp]
mcp_servers:
  - type: listener
    name: secure-mcp
    config:
      route:
        paths: [/mcp/secure]
    access:
      auth_strategies: [okta-oidc]
      metadata:
        endpoint: /mcp/secure/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/secure
        authorization_servers: [https://issuer.example.com]
    tools:
      - name: ping
        description: Ping
        method: GET
        path: /ping
        scheme: https
        host: secure.internal
`

func TestMCPOAuth2DerivesAudienceAndPassthrough(t *testing.T) {
	out, warnings, err := Convert([]byte(oidcWithAudienceAndPassthrough), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	// hide_credentials: false -> passthrough_credentials: true.
	require.Contains(t, s, "passthrough_credentials: true")
	// audience_required set -> validation enforced.
	require.Contains(t, s, "insecure_relaxed_audience_validation: false")
	// audience_required is only a signal; its value is not carried to the plugin.
	require.NotContains(t, s, "audience_required")
}

// input: one openid-connect strategy carrying token_exchange, referenced by an
// MCP server WITH access.metadata (which lowers to ai-mcp-oauth2, a plugin that
// cannot represent token_exchange). The control must not be dropped silently.
const mcpTokenExchangeDropInput = `
identity_providers:
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
      identity_providers: [okta-oidc-te]
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

// TestMCPMetadataDropsTokenExchange pins the security invariant: lowering an
// openid-connect strategy that configures token_exchange into an ai-mcp-oauth2
// plugin (triggered by access.metadata) must not silently drop the exchange.
// ai-mcp-oauth2 cannot represent token_exchange, so rather than emit a plugin
// that quietly omits it the conversion fails hard — in every mode.
func TestMCPMetadataDropsTokenExchange(t *testing.T) {
	t.Run("fails by default", func(t *testing.T) {
		_, _, err := Convert([]byte(mcpTokenExchangeDropInput), Options{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "token_exchange")
		require.Contains(t, err.Error(), "mcp-with-metadata")
	})

	t.Run("fails under strict", func(t *testing.T) {
		_, _, err := Convert([]byte(mcpTokenExchangeDropInput), Options{Strict: true})
		require.Error(t, err)
		require.Contains(t, err.Error(), "token_exchange")
	})
}
