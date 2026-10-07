package convert

import (
	"cmp"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

func (c *Converter) convertCustomPolicies() error {
	for _, policy := range c.src.CustomPolicies {
		if policy.Type != "streaming" || policy.Handler == "" {
			continue
		}
		if policy.Name == "" {
			return c.failAt("custom_policies.name", "custom policy name is required")
		}
		if policy.Schema == "" {
			return c.failAt("custom_policies.schema", "custom policy schema is required")
		}

		c.out.CustomPlugins = append(c.out.CustomPlugins, kong.CustomPlugin{
			ID:      cmp.Or(policy.ID, kong.StableUUID("custom_plugin:"+policy.Name)),
			Name:    policy.Name,
			Schema:  policy.Schema,
			Handler: policy.Handler,
		})
	}
	return nil
}
