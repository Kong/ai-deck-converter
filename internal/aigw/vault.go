package aigw

// Vault is an AI Gateway vault. type is the backend (konnect|env|aws|gcp|azure|
// conjur|hcv); config is backend-specific and passed through.
type Vault struct {
	ID          string         `yaml:"id,omitempty"`
	Type        string         `yaml:"type,omitempty"`
	Name        string         `yaml:"name,omitempty"`
	Description string         `yaml:"description,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Labels      Labels         `yaml:"labels,omitempty"`
}
