package revert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mcpOIDCNoMetadata is an MCP route carrying a plain openid-connect plugin
// (no ai-mcp-oauth2, no metadata) — the OIDC-without-metadata forward case.
const mcpOIDCNoMetadata = `
_format_version: "3.0"
services:
  - name: oidc-mcp
    host: localhost
    routes:
      - name: oidc-mcp-route
        paths: [/mcp/oidc]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
          - name: openid-connect
            config:
              issuer: https://issuer.example.com
              cache_tokens_salt: pepper
`

func TestRevertMCPOIDCWithoutMetadata(t *testing.T) {
	out, warnings, err := Revert([]byte(mcpOIDCNoMetadata), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	// A plain openid-connect plugin round-trips into an identity provider
	// reference with the full passthrough config preserved (no metadata).
	require.Contains(t, s, "identity_providers:")
	require.Contains(t, s, "type: openid-connect")
	require.Contains(t, s, "cache_tokens_salt: pepper", "full OIDC config should survive")
	require.NotContains(t, s, "metadata:", "no metadata should be reconstructed")
}

// mcpOAuth2MetadataOnly is an ai-mcp-oauth2 plugin with no client credentials
// (metadata-only): revert must reconstruct metadata but no identity provider.
const mcpOAuth2MetadataOnly = `
_format_version: "3.0"
services:
  - name: meta-only-mcp
    host: localhost
    routes:
      - name: meta-only-mcp-route
        paths:
          - /mcp/meta
          - /mcp/meta/.well-known/oauth-protected-resource
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
          - name: ai-mcp-oauth2
            config:
              resource: https://api.example.com/mcp/meta
              authorization_servers: [https://issuer.example.com]
              metadata_endpoint: /mcp/meta/.well-known/oauth-protected-resource
`

func TestRevertMCPOAuth2MetadataOnly(t *testing.T) {
	out, warnings, err := Revert([]byte(mcpOAuth2MetadataOnly), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "metadata:")
	require.Contains(t, s, "resource: https://api.example.com/mcp/meta")
	require.Contains(t, s, "endpoint: /mcp/meta/.well-known/oauth-protected-resource")
	// The .well-known path is stripped from the route paths.
	require.NotContains(t, strings.SplitN(s, "access:", 2)[0],
		"/mcp/meta/.well-known/oauth-protected-resource")
	// No client credentials => no synthesized identity provider.
	require.NotContains(t, s, "identity_providers:")
}
