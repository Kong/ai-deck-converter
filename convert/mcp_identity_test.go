package convert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// keyAuthWithMetadata references a key-auth identity provider together with
// access.metadata, which is unsupported (OAuth Protected Resource Metadata
// cannot be served by key-auth).
const keyAuthWithMetadata = `
identity_providers:
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
      identity_providers: [mcp-key-auth]
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
	}
}

// oidcOnUnsupportedListener puts identity/metadata access on an
// upstream-server MCP server, which does not support it.
const oidcOnUnsupportedListener = `
identity_providers:
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
      identity_providers: [okta-oidc]
      metadata:
        endpoint: /mcp/upstream/.well-known/oauth-protected-resource
        resource: https://api.example.com/mcp/upstream
        authorization_servers: [https://issuer.example.com]
    tools:
      - name: upstreamTool
        description: An upstream tool
`

func TestMCPIdentityUnsupportedListenerWarns(t *testing.T) {
	// Non-strict: unsupported listener type warns and skips auth (no error).
	out, warnings, err := Convert([]byte(oidcOnUnsupportedListener), Options{})
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	require.Contains(t, strings.Join(warnings, "\n"),
		"only supported for listener, conversion-listener, and passthrough-listener")
	require.NotContains(t, string(out), "ai-mcp-oauth2", "no auth plugin should be emitted")

	// Strict: the same condition becomes an error.
	_, _, err = Convert([]byte(oidcOnUnsupportedListener), Options{Strict: true})
	require.Error(t, err)
}

// oidcMetadataWithoutAuthServers omits metadata.authorization_servers so the
// OIDC issuer is used as the fallback.
const oidcMetadataWithoutAuthServers = `
identity_providers:
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
      identity_providers: [okta-oidc]
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
