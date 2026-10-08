package aigw

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

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

// rejectIdentityProviders returns an error if node has an identity_providers key.
// The key was renamed to auth_strategies.
// An ignored key would remove authentication from the output without an error.
func rejectIdentityProviders(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if key := node.Content[i]; key.Value == "identity_providers" {
			return fmt.Errorf("line %d: identity_providers was renamed to auth_strategies", key.Line)
		}
	}
	return nil
}
