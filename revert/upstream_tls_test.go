package revert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRevertUpstreamTLS(t *testing.T) {
	const entities = `
certificates:
  - {id: cert-id, cert: CERT, key: KEY}
ca_certificates:
  - {id: ca-id, cert: CA, tags: [ai-gateway-name:root]}
  - {id: unnamed-ca-id, cert: CA}
`
	tests := []struct {
		name        string
		tls         string
		wantWarning string
		wantOut     string
	}{
		{
			name:    "bare certificate id",
			tls:     `client_certificate: cert-id, ca_certificates: [ca-id]`,
			wantOut: "certificates:\n        - certificate-1\n      ca_certificates:\n        - root\n",
		},
		{
			name:        "unknown certificate id",
			tls:         `client_certificate: {id: missing}`,
			wantWarning: `MCP server "vendor-mcp" references unknown certificate id "missing"`,
		},
		{
			name:        "unknown CA certificate id",
			tls:         `ca_certificates: [missing]`,
			wantWarning: `MCP server "vendor-mcp" references unknown CA certificate id "missing"`,
		},
		{
			name: "CA certificate without name",
			tls:  `ca_certificates: [unnamed-ca-id]`,
			wantWarning: `MCP server "vendor-mcp" references CA certificate id "unnamed-ca-id", ` +
				`which has no ai-gateway-name tag; dropped`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte(`_format_version: "3.0"
services:
  - {name: vendor-mcp, url: "https://vendor.example.com/mcp", ` + tt.tls + `,
     routes: [{name: vendor-mcp-route, paths: [/mcp/vendor],
       plugins: [{name: ai-mcp-proxy, config: {mode: passthrough-listener}}]}]}
` + entities)

			out, warnings, err := Revert(src, Options{})
			require.NoError(t, err)
			require.Contains(t, string(out), tt.wantOut)
			if tt.wantWarning == "" {
				require.Empty(t, warnings)
				return
			}
			require.Contains(t, warnings, tt.wantWarning)

			_, _, err = Revert(src, Options{Strict: true})
			require.EqualError(t, err, tt.wantWarning)
		})
	}
}
