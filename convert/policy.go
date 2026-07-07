package convert

import (
	"fmt"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// convertGlobalPolicies emits each global policy as a top-level (global) Kong
// plugin. Non-global policies are instantiated per referencing entity instead.
func (c *Converter) convertGlobalPolicies() {
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		if p.Global != nil && *p.Global {
			c.out.Plugins = append(c.out.Plugins, policyPlugin(p, c.labelsToTags(p.Labels), true))
		}
	}
}

// scopedPlugins builds the plugins to nest under a referencing entity: one per
// non-global policy reference, plus an acl plugin when ACLs are present.
func (c *Converter) scopedPlugins(refs []string, acls aigw.ACLs) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		p := c.policies[ref]
		if p == nil {
			if err := c.warn("unknown policy reference %q", ref); err != nil {
				return nil, err
			}
			continue
		}
		if p.Global != nil && *p.Global {
			continue // emitted once at the top level
		}
		plugins = append(plugins, policyPlugin(p, nil, false))
	}
	if !acls.IsEmpty() {
		// A Kong acl plugin enforces only_one_of {config.allow, config.deny}; an
		// AI Gateway acl that sets both is not representable as one valid plugin,
		// so reject it rather than emit config the gateway will refuse to load.
		if len(acls.Allow) > 0 && len(acls.Deny) > 0 {
			return nil, fmt.Errorf("acl policy sets both allow (%v) and deny (%v), but a Kong acl plugin permits exactly one; set only allow or only deny", acls.Allow, acls.Deny)
		}
		plugins = append(plugins, aclPlugin(acls))
	}
	return plugins, nil
}

func policyPlugin(p *aigw.Policy, tags []string, preserveID bool) kong.Plugin {
	plugin := kong.Plugin{Name: p.Type, Config: p.Config, Tags: tags}
	if preserveID {
		plugin.ID = p.ID
	}
	if p.Enabled != nil && !*p.Enabled {
		disabled := false
		plugin.Enabled = &disabled
	}
	return plugin
}
