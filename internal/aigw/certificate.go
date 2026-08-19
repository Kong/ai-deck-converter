package aigw

// Certificate is an AI Gateway certificate: a PEM-encoded certificate and its
// matching private key, optionally paired with an alternative certificate that
// uses a different key algorithm (e.g. RSA plus ECDSA). name is the stable
// human-readable identifier; it is immutable in the AI Gateway API.
type Certificate struct {
	ID      string `yaml:"id,omitempty"`
	Name    string `yaml:"name,omitempty"`
	Cert    string `yaml:"cert,omitempty"`
	Key     string `yaml:"key,omitempty"`
	CertAlt string `yaml:"cert_alt,omitempty"`
	KeyAlt  string `yaml:"key_alt,omitempty"`
	Labels  Labels `yaml:"labels,omitempty"`
}
