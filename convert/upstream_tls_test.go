package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const upstreamTLSEntities = `
certificates:
  - {id: cert-id, name: client, cert: CERT, key: KEY}
ca_certificates:
  - {id: ca-id, name: root, cert: CA}
`

func TestConvertUpstreamTLSIssues(t *testing.T) {
	tests := []struct {
		name         string
		config       string
		wantSeverity ConversionIssueSeverity
		wantReason   string
		wantCode     ConversionIssueCode
		wantFields   []string
		wantCert     map[string]string
		wantCAs      []string
	}{
		{
			name:         "http url",
			config:       `{url: http://agent.internal, certificates: [client], ca_certificates: [root]}`,
			wantSeverity: Error,
			wantReason:   `agent "a" sets certificates or ca_certificates, but its url "http://agent.internal" is not https`,
			wantCode:     ConfigFieldInvalid,
			wantFields:   []string{"config.certificates", "config.ca_certificates"},
		},
		{
			name:         "unknown certificate",
			wantSeverity: Error,
			config:       `{url: https://agent.internal, certificates: [missing], ca_certificates: [root]}`,
			wantReason:   `agent "a" references unknown certificate "missing"`,
			wantCode:     UndefinedReference,
			wantFields:   []string{"config.certificates[0]"},
			wantCAs:      []string{"ca-id"},
		},
		{
			name:         "unknown CA certificate",
			wantSeverity: Error,
			config:       `{url: https://agent.internal, certificates: [client], ca_certificates: [missing, root]}`,
			wantReason:   `agent "a" references unknown CA certificate "missing"`,
			wantCode:     UndefinedReference,
			wantFields:   []string{"config.ca_certificates[0]"},
			wantCert:     map[string]string{"id": "cert-id"},
			wantCAs:      []string{"ca-id"},
		},
		{
			name:         "CA certificate name in certificates",
			wantSeverity: Error,
			config:       `{url: https://agent.internal, certificates: [root]}`,
			wantReason:   `agent "a" references unknown certificate "root"`,
			wantCode:     UndefinedReference,
			wantFields:   []string{"config.certificates[0]"},
		},
		{
			name:         "more than one certificate",
			wantSeverity: Warning,
			config:       `{url: https://agent.internal, certificates: [client, other]}`,
			wantReason:   `agent "a" sets 2 certificates, but a Service presents one client certificate; only "client" is used`,
			wantCode:     ConfigFieldDropped,
			wantFields:   []string{"config.certificates"},
			wantCert:     map[string]string{"id": "cert-id"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("agents:\n  - {type: http, name: a, config: " + tt.config + "}\n" + upstreamTLSEntities)

			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			issue := requireIssue(t, result.Issues, tt.wantSeverity, tt.wantCode, kindAgent, "a", tt.wantFields...)
			require.Equal(t, tt.wantReason, issue.Reason)
			doc := result.Output
			require.Len(t, doc.Services, 1)
			require.Equal(t, tt.wantCert, map[string]string(doc.Services[0].ClientCertificate))
			require.Equal(t, tt.wantCAs, doc.Services[0].CACertificates)
		})
	}
}
