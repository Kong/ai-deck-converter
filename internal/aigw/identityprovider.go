package aigw

// IdentityProvider is an AI Gateway identity provider. Its `type` equals a
// Kong authentication plugin name (key-auth or openid-connect) and its config
// is passed through. Identity providers are referenced by name from a
// Model's access.identity_providers list and instantiated as a scoped
// authentication plugin on that model's route.
type IdentityProvider struct {
	ID          string         `yaml:"id,omitempty"`
	Type        string         `yaml:"type,omitempty"`
	DisplayName string         `yaml:"display_name,omitempty"`
	Name        string         `yaml:"name,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Labels      Labels         `yaml:"labels,omitempty"`
}
