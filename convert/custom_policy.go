package convert

import (
	"cmp"
	"regexp"
	"strconv"

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

// luaPriority matches a Kong plugin handler's PRIORITY field, e.g. the 1000
// in `local plugin = { PRIORITY = 1000, VERSION = "1.0.0" }`.
var luaPriority = regexp.MustCompile(`PRIORITY\s*=\s*(-?\d+)`)

// customPolicyPriority returns the Lua PRIORITY that the custom policy named
// pluginName declares in its handler. aimap.PluginPriority has no entry for a
// custom plugin, so this is the only way the converter can tell whether one
// outranks ai-model-selector. It returns false when no custom policy of that
// name converts, or its handler sets no PRIORITY.
func (c *Converter) customPolicyPriority(pluginName string) (int, bool) {
	for _, cp := range c.src.CustomPolicies {
		if cp.Name != pluginName || cp.Type != "streaming" || cp.Handler == "" {
			continue
		}
		m := luaPriority.FindStringSubmatch(cp.Handler)
		if m == nil {
			return 0, false
		}
		priority, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, false
		}
		return priority, true
	}
	return 0, false
}
