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
	// A plain openid-connect plugin round-trips into an auth strategy plus the
	// access reference to it, with the full passthrough config preserved (no
	// metadata).
	require.Contains(t, s, "auth_strategies:")
	require.Contains(t, s, "type: openid-connect")
	require.Contains(t, s, "cache_tokens_salt: pepper", "full OIDC config should survive")
	require.NotContains(t, s, "metadata:", "no metadata should be reconstructed")
}

// mcpOAuth2MetadataOnly is an ai-mcp-oauth2 plugin with no client credentials
// (metadata-only): revert must reconstruct metadata but no auth strategy.
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
	// No client credentials => no synthesized auth strategy.
	require.NotContains(t, s, "auth_strategies:")
}

// mcpOAuth2MappedFields carries the mapped identity fields on an ai-mcp-oauth2
// plugin; revert must lift them back onto a synthesized openid-connect provider
// (re-wrapping the scalar/path first-of kinds and renaming jwt_claims_leeway).
const mcpOAuth2MappedFields = `
_format_version: "3.0"
services:
  - name: mapped-mcp
    host: localhost
    routes:
      - name: mapped-mcp-route
        paths:
          - /mcp/mapped
          - /mcp/mapped/.well-known/oauth-protected-resource
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
          - name: ai-mcp-oauth2
            config:
              resource: https://api.example.com/mcp/mapped
              authorization_servers: [https://issuer.example.com]
              metadata_endpoint: /mcp/mapped/.well-known/oauth-protected-resource
              client_auth: client_secret_post
              cache_introspection: true
              jwt_claims_leeway: 30
              timeout: 5000
              consumer_claim: [sub]
              proxy_config:
                http_proxy_host: proxy.example.com
                http_proxy_port: 8080
                https_proxy_host: secure-proxy.example.com
                https_proxy_port: 8443
                proxy_scheme: http
                auth_username: test
                auth_password: pass
                no_proxy: localhost,.example.com
              upstream_headers:
                - header: X-User-ID
                  path: [user, id]
`

func TestRevertMCPOAuth2MapsIdentityFields(t *testing.T) {
	out, warnings, err := Revert([]byte(mcpOAuth2MappedFields), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "type: openid-connect")
	// Passthrough verbatim.
	require.Contains(t, s, "cache_introspection: true")
	require.Contains(t, s, "timeout: 5000")
	require.Contains(t, s, "http_proxy: http://proxy.example.com:8080")
	require.Contains(t, s, "https_proxy: http://secure-proxy.example.com:8443")
	require.Contains(t, s, "http_proxy_authorization: Basic dGVzdDpwYXNz")
	require.Contains(t, s, "https_proxy_authorization: Basic dGVzdDpwYXNz")
	require.Contains(t, s, "no_proxy: localhost,.example.com")
	require.Contains(t, s, "upstream_headers:")
	require.Contains(t, s, "header: X-User-ID")
	require.Contains(t, s, "path:\n            - user\n            - id")
	// jwt_claims_leeway lifts back to the OIDC leeway key.
	require.Contains(t, s, "leeway: 30")
	require.NotContains(t, s, "jwt_claims_leeway")
	// FirstOfArray scalar re-wraps into a single-element array.
	require.Contains(t, s, "client_auth:\n        - client_secret_post")
	// FirstOfPaths re-wraps the single path into an array-of-paths.
	require.Contains(t, s, "consumer_claims:\n        - - sub")
	// The identity fields belong to the provider, not the metadata block.
	require.Contains(t, s, "metadata:")
}

func TestProxyURLFormatsPortlessIPv6Hosts(t *testing.T) {
	require.Equal(t, "http://[2001:db8::1]", proxyURL("http", "2001:db8::1", nil))
	require.Equal(t, "https://[fe80::1%25en0]", proxyURL("https", "fe80::1%en0", nil))
}

// mcpOAuth2AudiencePassthrough exercises the reverse of the two derived fields:
// passthrough_credentials -> hide_credentials, and a non-relaxed audience flag
// -> audience_required reconstructed from the resource.
const mcpOAuth2AudiencePassthrough = `
_format_version: "3.0"
services:
  - name: secure-mcp
    host: localhost
    routes:
      - name: secure-mcp-route
        paths:
          - /mcp/secure
          - /mcp/secure/.well-known/oauth-protected-resource
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
          - name: ai-mcp-oauth2
            config:
              resource: https://api.example.com/mcp/secure
              authorization_servers: [https://issuer.example.com]
              metadata_endpoint: /mcp/secure/.well-known/oauth-protected-resource
              client_id: mcp-client
              passthrough_credentials: true
              insecure_relaxed_audience_validation: false
`

func TestRevertMCPOAuth2AudienceAndPassthrough(t *testing.T) {
	out, warnings, err := Revert([]byte(mcpOAuth2AudiencePassthrough), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "type: openid-connect")
	// passthrough_credentials: true -> hide_credentials: false.
	require.Contains(t, s, "hide_credentials: false")
	// Non-relaxed audience flag -> audience_required reconstructed from resource.
	require.Contains(t, s, "audience_required:")
	require.Contains(t, s, "- https://api.example.com/mcp/secure")
	// The plugin flags themselves are not leaked into the provider config.
	require.NotContains(t, s, "passthrough_credentials")
	require.NotContains(t, s, "insecure_relaxed_audience_validation")
}

// mcpOAuth2RelaxedDefault confirms the relaxed default (true) reconstructs no
// audience_required.
const mcpOAuth2RelaxedDefault = `
_format_version: "3.0"
services:
  - name: relaxed-mcp
    host: localhost
    routes:
      - name: relaxed-mcp-route
        paths:
          - /mcp/relaxed
          - /mcp/relaxed/.well-known/oauth-protected-resource
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
          - name: ai-mcp-oauth2
            config:
              resource: https://api.example.com/mcp/relaxed
              authorization_servers: [https://issuer.example.com]
              metadata_endpoint: /mcp/relaxed/.well-known/oauth-protected-resource
              client_id: mcp-client
              insecure_relaxed_audience_validation: true
`

func TestRevertMCPOAuth2RelaxedDefaultDropsAudience(t *testing.T) {
	out, warnings, err := Revert([]byte(mcpOAuth2RelaxedDefault), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	s := string(out)
	require.Contains(t, s, "type: openid-connect")
	require.NotContains(t, s, "audience_required",
		"relaxed validation (true) reconstructs no audience_required")
}
