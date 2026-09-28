package convert

import (
	"github.com/Kong/ai-deck-converter/internal/kong"
)

func (c *Converter) convertCustomPolicies() error {
	for _, policy := range c.src.CustomPolicies {
		if policy.Handler == "" {
			continue
		}
		if policy.Name == "" {
			return c.failAt("custom_policies.name", "custom policy name is required")
		}
		if policy.Schema == "" {
			return c.failAt("custom_policies.schema", "custom policy schema is required")
		}

		c.customPlugins = append(c.customPlugins, kong.DBLessCustomPlugin{
			ID:      firstNonEmpty(policy.ID, stableUUID("custom_plugin:"+policy.Name)),
			Name:    policy.Name,
			Enabled: true,
			Schema:  policy.Schema,
			Handler: policy.Handler,
		})
	}

	return nil
}
