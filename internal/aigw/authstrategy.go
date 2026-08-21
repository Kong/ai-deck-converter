package aigw

import "gopkg.in/yaml.v3"

// AuthStrategy is an AI Gateway auth strategy. Its `type` equals a Kong
// authentication plugin name (key-auth or openid-connect) and its config is
// passed through. Auth strategies are referenced by name from an entity's
// access.auth_strategies list and instantiated as a scoped authentication
// plugin on that entity's route.
//
// The entity was previously called an "identity provider". The deprecated
// `identity_providers` spellings — both the document key (see
// Document.UnmarshalYAML) and the access reference list (see
// deprecatedAuthStrategyRefs) — are still accepted on input and never emitted.
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

// deprecatedAuthStrategyRefs is the superseded spelling of an access block's
// auth_strategies reference list.
type deprecatedAuthStrategyRefs struct {
	IdentityProviders []string `yaml:"identity_providers,omitempty"`
}

// appendDeprecatedAuthStrategyRefs decodes the deprecated identity_providers
// key out of an access-block node and appends it to refs, which holds whatever
// the current auth_strategies key carried. References under the current key
// come first; a block may set either (or, transitionally, both).
func appendDeprecatedAuthStrategyRefs(node *yaml.Node, refs []string) ([]string, error) {
	var deprecated deprecatedAuthStrategyRefs
	if err := node.Decode(&deprecated); err != nil {
		return nil, err
	}
	return append(refs, deprecated.IdentityProviders...), nil
}
