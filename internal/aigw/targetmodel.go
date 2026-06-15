package aigw

import "gopkg.in/yaml.v3"

// TargetModel is one backend model a Model routes to. Provider references a
// Provider by name (a bare string, per the Admin API); Config carries the
// provider type plus model options.
type TargetModel struct {
	Name              string            `yaml:"name,omitempty"`
	Weight            *int              `yaml:"weight,omitempty"`
	SemanticDesc      string            `yaml:"semantic_description,omitempty"`
	AllowAuthOverride *bool             `yaml:"allow_auth_override,omitempty"`
	Provider          string            `yaml:"provider,omitempty"`
	Config            TargetModelConfig `yaml:"config,omitempty"`
}

// TargetModelConfig is discriminated by provider `type`. The remaining fields are
// model options (temperature, top_p, upstream_url, provider-specific keys, ...);
// they are retained as a generic map and translated/renamed in the convert layer.
type TargetModelConfig struct {
	Type    string
	Options map[string]any
}

// UnmarshalYAML extracts the provider type discriminator and keeps the rest of
// the body as the options map.
func (c *TargetModelConfig) UnmarshalYAML(node *yaml.Node) error {
	var m map[string]any
	if err := node.Decode(&m); err != nil {
		return err
	}
	if t, ok := m["type"].(string); ok {
		c.Type = t
		delete(m, "type")
	}
	c.Options = m
	return nil
}

// MarshalYAML re-merges the provider type discriminator with the options map
// (inverse of UnmarshalYAML, used by the reverse converter).
func (c TargetModelConfig) MarshalYAML() (any, error) {
	m := make(map[string]any, len(c.Options)+1)
	for k, v := range c.Options {
		m[k] = v
	}
	if c.Type != "" {
		m["type"] = c.Type
	}
	return m, nil
}
