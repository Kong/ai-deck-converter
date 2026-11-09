package convert

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

// instanceNameDisallowed excludes "." from a segment.
// "." joins the segments, so a dot in a segment can make two names equal.
var instanceNameDisallowed = regexp.MustCompile(`[^0-9A-Za-z_~-]+`)

// sanitizeInstanceNameSegment strips a leading "@" and changes each run of
// disallowed characters to one "-".
func sanitizeInstanceNameSegment(s string) string {
	s = strings.TrimPrefix(s, "@")
	s = instanceNameDisallowed.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func buildInstanceName(pluginName string, qualifiers ...string) string {
	segments := []string{pluginName}
	for _, q := range qualifiers {
		if s := sanitizeInstanceNameSegment(q); s != "" {
			segments = append(segments, s)
		}
	}
	return strings.Join(segments, ".")
}

// namedPlugin is a plugin of the output and the scope that it is nested in.
type namedPlugin struct {
	plugin      *kong.Plugin
	qualifiers  []string
	description string
}

// applyPluginInstanceNames gives each plugin of the output an instance_name.
// The name comes from the plugin type and the names of its scope.
// A route name can have an order-based counter, for example openai-chat-2.
// Kong requires a unique instance_name per workspace, so a later plugin with
// a used name gets no name and a warning.
func (c *Converter) applyPluginInstanceNames() {
	seen := map[string]string{}
	for _, np := range c.outputPlugins() {
		p := np.plugin
		name := p.InstanceName
		if name == "" {
			name = buildInstanceName(p.Name, np.qualifiers...)
		}
		if first, ok := seen[name]; ok {
			p.InstanceName = ""
			c.warn(instanceNameIssueSource(p), ConfigFieldDropped,
				"%s gets the instance_name %q, but %s already uses it. The plugin gets no instance_name.",
				np.description, name, first)
			continue
		}
		seen[name] = np.description
		p.InstanceName = name
	}
}

func (c *Converter) outputPlugins() []namedPlugin {
	doc := c.out
	var plugins []namedPlugin
	nested := func(list []kong.Plugin, kind, parent string) {
		for i := range list {
			p := &list[i]
			plugins = append(plugins, namedPlugin{
				plugin:      p,
				qualifiers:  []string{parent},
				description: fmt.Sprintf("%s on %s %q", p.Name, kind, parent),
			})
		}
	}
	for i := range doc.Plugins {
		p := &doc.Plugins[i]
		plugins = append(plugins, namedPlugin{
			plugin:      p,
			qualifiers:  topLevelPluginQualifiers(p),
			description: topLevelPluginDescription(p),
		})
	}
	for si := range doc.Services {
		svc := &doc.Services[si]
		nested(svc.Plugins, "service", svc.Name)
		for ri := range svc.Routes {
			nested(svc.Routes[ri].Plugins, "route", svc.Routes[ri].Name)
		}
	}
	for ri := range doc.Routes {
		nested(doc.Routes[ri].Plugins, "route", doc.Routes[ri].Name)
	}
	for ci := range doc.Consumers {
		consumer := &doc.Consumers[ci]
		nested(consumer.Plugins, "consumer", cmp.Or(consumer.Username, consumer.CustomID))
	}
	for gi := range doc.ConsumerGroups {
		nested(doc.ConsumerGroups[gi].Plugins, "consumer group", doc.ConsumerGroups[gi].Name)
	}
	return plugins
}

// topLevelPluginQualifiers gives no qualifier to a global plugin.
// Revert does not keep the name of a global policy, so a name from it does
// not survive a round trip.
// Kong allows one global plugin of each type.
func topLevelPluginQualifiers(p *kong.Plugin) []string {
	var qualifiers []string
	if p.Model != nil {
		qualifiers = append(qualifiers, string(*p.Model))
	}
	switch {
	case p.Route != nil:
		qualifiers = append(qualifiers, string(*p.Route))
	case p.Service != nil:
		qualifiers = append(qualifiers, string(*p.Service))
	case p.Consumer != nil:
		qualifiers = append(qualifiers, string(*p.Consumer))
	case p.ConsumerGroup != nil:
		qualifiers = append(qualifiers, string(*p.ConsumerGroup))
	}
	return qualifiers
}

func topLevelPluginDescription(p *kong.Plugin) string {
	var scope string
	switch {
	case p.Route != nil:
		scope = fmt.Sprintf(" on route %q", string(*p.Route))
	case p.Service != nil:
		scope = fmt.Sprintf(" on service %q", string(*p.Service))
	case p.Consumer != nil:
		scope = fmt.Sprintf(" on consumer %q", string(*p.Consumer))
	case p.ConsumerGroup != nil:
		scope = fmt.Sprintf(" on consumer group %q", string(*p.ConsumerGroup))
	}
	switch {
	case p.Model != nil:
		return fmt.Sprintf("%s for model %q%s", p.Name, string(*p.Model), scope)
	case scope == "":
		return "global " + p.Name
	default:
		return p.Name + scope
	}
}

// instanceNameIssueSource changes the "identity_provider" source of an auth
// plugin to the auth_strategy issue kind.
func instanceNameIssueSource(p *kong.Plugin) ConversionIssueSource {
	switch {
	case p.Source == nil || p.Source.EntityType == "":
		return issueSource(kindConversionOption, "plugin_instance_names")
	case p.Source.EntityType == "identity_provider":
		return issueSource(kindAuthStrategy, p.Source.EntityName)
	default:
		return issueSource(p.Source.EntityType, p.Source.EntityName)
	}
}
