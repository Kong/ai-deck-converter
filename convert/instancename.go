package convert

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

// instanceNameDisallowed matches every run of characters Kong's
// validate_utf8_name rejects (kong-ee:kong/db/schema/typedefs.lua), restricted
// here to ASCII so callers never have to reason about the multibyte range it
// also allows.
var instanceNameDisallowed = regexp.MustCompile(`[^0-9A-Za-z._~-]+`)

// sanitizeInstanceNameSegment makes s safe to use as one dot-joined segment of
// a Kong instance_name: a leading "@" is stripped (aliases are commonly
// written "@openai/gpt-4o-mini"), every run of disallowed characters
// collapses to a single "-", and leading/trailing "-" are trimmed. The "."
// separator itself is excluded from the allowed set so it can never appear
// inside a sanitized segment and be mistaken for one.
func sanitizeInstanceNameSegment(s string) string {
	s = strings.TrimPrefix(s, "@")
	s = instanceNameDisallowed.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// buildInstanceName joins a plugin's Kong plugin name with its non-empty,
// sanitized qualifiers using ".".
func buildInstanceName(pluginName string, qualifiers ...string) string {
	segments := make([]string, 0, len(qualifiers)+1)
	segments = append(segments, pluginName)
	for _, q := range qualifiers {
		if s := sanitizeInstanceNameSegment(q); s != "" {
			segments = append(segments, s)
		}
	}
	return strings.Join(segments, ".")
}

// applyPluginInstanceNames stamps a deterministic instance_name on every
// plugin in the assembled document. Names are a pure function of each
// plugin's own kind plus the model/route/service/consumer scope that produced
// it -- never of position -- so they stay stable when unrelated entities are
// added, removed, or reordered elsewhere in the document. An
// author-supplied Plugin.InstanceName (were the AI Gateway entity model ever
// to grow one) is honored as-is and never overwritten.
//
// instance_name is unique per workspace across every plugin, regardless of
// type (kong-ee:kong/db/schema/entities/plugins.lua), so a collision is a
// hard error naming both offending plugins rather than a silently
// order-dependent numeric suffix.
func applyPluginInstanceNames(doc *kong.Document) error {
	seen := map[string]string{}

	claim := func(p *kong.Plugin, describe func() string, qualifiers ...string) error {
		name := p.InstanceName
		if name == "" {
			name = buildInstanceName(p.Name, qualifiers...)
		}
		if existing, ok := seen[name]; ok {
			return fmt.Errorf(
				"duplicate instance_name %q: %s and %s both resolve to it; "+
					"give one of them a distinct name or route", name, existing, describe())
		}
		seen[name] = describe()
		p.InstanceName = name
		return nil
	}

	for i := range doc.Plugins {
		p := &doc.Plugins[i]
		desc := func() string { return topLevelPluginDescription(p) }
		if err := claim(p, desc, topLevelPluginQualifiers(p)...); err != nil {
			return err
		}
	}
	for si := range doc.Services {
		svc := &doc.Services[si]
		for pi := range svc.Plugins {
			p := &svc.Plugins[pi]
			desc := fmt.Sprintf("%s nested under service %q", p.Name, svc.Name)
			if err := claim(p, func() string { return desc }, svc.Name); err != nil {
				return err
			}
		}
		for ri := range svc.Routes {
			route := &svc.Routes[ri]
			for pi := range route.Plugins {
				p := &route.Plugins[pi]
				desc := fmt.Sprintf("%s nested under route %q", p.Name, route.Name)
				if err := claim(p, func() string { return desc }, route.Name); err != nil {
					return err
				}
			}
		}
	}
	for ci := range doc.Consumers {
		consumer := &doc.Consumers[ci]
		for pi := range consumer.Plugins {
			p := &consumer.Plugins[pi]
			desc := fmt.Sprintf("%s nested under consumer %q", p.Name, consumer.Username)
			if err := claim(p, func() string { return desc }, consumer.Username); err != nil {
				return err
			}
		}
	}
	for gi := range doc.ConsumerGroups {
		group := &doc.ConsumerGroups[gi]
		for pi := range group.Plugins {
			p := &group.Plugins[pi]
			desc := fmt.Sprintf("%s nested under consumer group %q", p.Name, group.Name)
			if err := claim(p, func() string { return desc }, group.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// topLevelPluginQualifiers derives the naming qualifiers for a top-level
// plugin from its own foreign-key fields: the model it targets (when scoped
// to one) plus whichever single scope FK identifies where it applies. A
// top-level plugin with none of these set is a global plugin (e.g. a global
// policy, convert/policy.go): it gets no extra qualifier at all, deliberately
// -- Plugin.Source is conversion-only metadata revert cannot reconstruct (a
// reverted document names a global policy after its type, not its original
// AI Gateway name), so qualifying on it would make the name depend on
// information that doesn't survive a round trip. Kong itself allows only one
// global instance of a given plugin type, so the bare plugin name is already
// unique here.
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
	switch {
	case p.Model != nil && p.Route != nil:
		return fmt.Sprintf("%s for model %q on route %q", p.Name, string(*p.Model), string(*p.Route))
	case p.Route != nil:
		return fmt.Sprintf("%s on route %q", p.Name, string(*p.Route))
	case p.Service != nil:
		return fmt.Sprintf("%s on service %q", p.Name, string(*p.Service))
	case p.Consumer != nil:
		return fmt.Sprintf("%s on consumer %q", p.Name, string(*p.Consumer))
	case p.ConsumerGroup != nil:
		return fmt.Sprintf("%s on consumer group %q", p.Name, string(*p.ConsumerGroup))
	default:
		return fmt.Sprintf("global %s", p.Name)
	}
}
