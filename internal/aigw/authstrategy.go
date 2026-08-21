package aigw

// AuthStrategy is an AI Gateway auth strategy. Its `type` equals a Kong
// authentication plugin name (key-auth or openid-connect) and its config is
// passed through. Auth strategies are referenced by name from a Model's
// access.identity_providers list and instantiated as a scoped authentication
// plugin on that model's route.
//
// The entity was previously called an "identity provider"; the deprecated
// `identity_providers` document key is still accepted on input (see
// Document.UnmarshalYAML).
type AuthStrategy struct {
	ID          string         `yaml:"id,omitempty"`
	Type        string         `yaml:"type,omitempty"`
	DisplayName string         `yaml:"display_name,omitempty"`
	Name        string         `yaml:"name,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Labels      Labels         `yaml:"labels,omitempty"`
}

// IdentityProvider is the former name of AuthStrategy.
//
// Deprecated: use AuthStrategy.
type IdentityProvider = AuthStrategy
