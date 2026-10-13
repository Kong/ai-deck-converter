package convert

import (
	"cmp"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

func (c *Converter) convertCustomPolicies() {
	for _, policy := range c.src.CustomPolicies {
		if policy.Type != "streaming" || policy.Handler == "" {
			continue
		}
		src := issueSource(kindCustomPolicy, policy.Name)
		c.convertEntity(src, func() error {
			if policy.Name == "" {
				return c.fail(src.at("name"), MissingRequiredConfigField, "custom policy name is required")
			}
			if policy.Schema == "" {
				return c.fail(src.at("schema"), MissingRequiredConfigField, "custom policy schema is required")
			}

			c.out.CustomPlugins = append(c.out.CustomPlugins, kong.CustomPlugin{
				ID:      cmp.Or(policy.ID, kong.StableUUID("custom_plugin:"+policy.Name)),
				Name:    policy.Name,
				Schema:  policy.Schema,
				Handler: policy.Handler,
			})
			return nil
		})
	}
}
