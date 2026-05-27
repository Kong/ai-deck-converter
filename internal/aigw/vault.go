package aigw

// Vault is an AI Gateway vault. type is the backend (konnect|env|aws|gcp|azure|
// conjur|hcv); config is backend-specific and passed through.
type Vault struct {
	Type        string         `yaml:"type"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Config      map[string]any `yaml:"config"`
	Labels      Labels         `yaml:"labels"`
}
