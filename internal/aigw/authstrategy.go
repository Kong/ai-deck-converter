package aigw

// AuthStrategy is an AI Gateway auth strategy. Its `type` equals a Kong
// authentication plugin name (key-auth or openid-connect) and its config is
// passed through. Auth strategies are referenced by name from an entity's
// access.auth_strategies list and instantiated as a scoped authentication
// plugin on that entity's route.
type AuthStrategy struct {
	ID          string         `yaml:"id,omitempty"`
	Type        string         `yaml:"type,omitempty"`
	DisplayName string         `yaml:"display_name,omitempty"`
	Name        string         `yaml:"name,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Labels      Labels         `yaml:"labels,omitempty"`
}
