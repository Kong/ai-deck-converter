package aigw

// CACertificate is an AI Gateway CA certificate: a trusted CA used to verify
// the validity of a client or server certificate.
type CACertificate struct {
	ID         string `yaml:"id,omitempty"`
	Name       string `yaml:"name,omitempty"`
	Cert       string `yaml:"cert,omitempty"`
	CertDigest string `yaml:"cert_digest,omitempty"`
	Labels     Labels `yaml:"labels,omitempty"`
}
