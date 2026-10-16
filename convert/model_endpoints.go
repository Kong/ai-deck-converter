package convert

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// modelLists holds the formats and capabilities that a model uses. A list
// that holds aimap.Wildcard ignores its other values.
type modelLists struct {
	anyFormat bool
	// formats are normalized and unique. They are empty when anyFormat is set.
	formats       []string
	anyCapability bool
	// capabilities are canonical and unique. They are empty when anyCapability
	// is set.
	capabilities []string
}

// effectiveLists returns the lists that m uses. It does not change m.
func effectiveLists(m *aigw.Model) modelLists {
	var l modelLists
	l.anyFormat = slices.ContainsFunc(m.Formats, isWildcardFormat)
	if !l.anyFormat {
		for _, f := range m.Formats {
			format := aimap.NormalizeFormat(f.Type)
			if format == "" {
				format = aimap.DefaultLLMFormat
			}
			if !slices.Contains(l.formats, format) {
				l.formats = append(l.formats, format)
			}
		}
		if len(l.formats) == 0 {
			l.formats = []string{aimap.DefaultLLMFormat}
		}
	}
	l.anyCapability = slices.Contains(m.Capabilities, aimap.Wildcard)
	if !l.anyCapability {
		for _, raw := range m.Capabilities {
			for _, capability := range aimap.NormalizeCapability(raw) {
				if !slices.Contains(l.capabilities, capability) {
					l.capabilities = append(l.capabilities, capability)
				}
			}
		}
	}
	return l
}

// fixedOrder reports whether the pairs of the model use pair order. Otherwise
// they use list order, so the output of a model with one format stays as it is.
func (l modelLists) fixedOrder() bool {
	return l.anyFormat || l.anyCapability || len(l.formats) > 1
}

func isWildcardFormat(f aigw.Format) bool { return f.Type == aimap.Wildcard }

// checkModelLists warns for each value next to aimap.Wildcard. It fails m for
// an unknown format.
func (c *Converter) checkModelLists(src ConversionIssueSource, m *aigw.Model) error {
	if slices.Contains(m.Capabilities, aimap.Wildcard) {
		for i, raw := range m.Capabilities {
			if raw != aimap.Wildcard {
				c.warn(src.at(fmt.Sprintf("capabilities[%d]", i)), ConfigFieldIgnored,
					"model %q sets capability %q next to %q. This will be ignored, because %q selects "+
						"every capability.", m.Name, raw, aimap.Wildcard, aimap.Wildcard)
			}
		}
	}
	anyFormat := slices.ContainsFunc(m.Formats, isWildcardFormat)
	known := aimap.Formats()
	for i, f := range m.Formats {
		switch {
		case f.Type == aimap.Wildcard:
		case anyFormat:
			c.warn(src.at(fmt.Sprintf("formats[%d]", i)), ConfigFieldIgnored,
				"model %q sets format %q next to %q. This will be ignored, because %q selects "+
					"every format.", m.Name, f.Type, aimap.Wildcard, aimap.Wildcard)
		case f.Type != "" && !slices.Contains(known, aimap.NormalizeFormat(f.Type)):
			return c.fail(src.at(fmt.Sprintf("formats[%d].type", i)), ConfigFieldInvalid,
				"model %q: format %q is not a known format. The known formats are %s.",
				m.Name, f.Type, strings.Join(known, ", "))
		}
	}
	return nil
}

// formatField returns the path of the entry of m.Formats that selects format.
func formatField(m *aigw.Model, format string) string {
	if i := slices.IndexFunc(m.Formats, isWildcardFormat); i >= 0 {
		return fmt.Sprintf("formats[%d]", i)
	}
	for i, f := range m.Formats {
		if aimap.NormalizeFormat(f.Type) == format {
			return fmt.Sprintf("formats[%d]", i)
		}
	}
	return "formats"
}

// modelEndpoint is one (format, capability) pair of a target and the
// endpoints that serve it.
type modelEndpoint struct {
	format     string
	section    string
	capability string
	specs      []aimap.EndpointSpec
}

// modelEndpoints returns the endpoints that a target of provider type
// providerType serves for m. A passthrough model serves one endpoint with an
// empty capability.
func (c *Converter) modelEndpoints(
	src ConversionIssueSource, m *aigw.Model, l modelLists, tm *aigw.TargetModel, providerType string,
) ([]modelEndpoint, error) {
	if isPassthrough(m) {
		return []modelEndpoint{{
			format:  aimap.PassthroughFormat,
			section: aimap.SectionFor(aimap.PassthroughFormat, providerType),
			specs:   []aimap.EndpointSpec{aimap.PassthroughEndpoint},
		}}, nil
	}
	formats := c.targetFormats(src, m, l, tm, providerType)
	var endpoints []modelEndpoint
	for _, format := range formats {
		for _, capability := range c.formatCapabilities(src, m, l, format, providerType) {
			if !servesPair(format, capability, providerType) {
				continue
			}
			section := aimap.SectionFor(format, providerType)
			specs, _ := aimap.EndpointsFor(section, capability)
			endpoints = append(endpoints, modelEndpoint{
				format: format, section: section, capability: capability, specs: specs,
			})
		}
	}
	if err := c.checkServedCapabilities(src, m, l, endpoints, providerType); err != nil {
		return nil, err
	}
	return c.dropEqualPaths(src, m, l, endpoints), nil
}

// targetFormats returns the formats of m for one target. A mantle bedrock
// target does not serve the bedrock format.
func (c *Converter) targetFormats(
	src ConversionIssueSource, m *aigw.Model, l modelLists, tm *aigw.TargetModel, providerType string,
) []string {
	var formats []string
	if l.anyFormat {
		for _, format := range aimap.WildcardFormats() {
			if f, gated := aimap.FormatFeature(format); gated && !c.supports(f) {
				field := formatField(m, format)
				c.reportUnsupported(src.at(field), Warning, f, fmt.Sprintf("%s: %s", field, format))
				continue
			}
			formats = append(formats, format)
		}
	} else {
		formats = slices.Clone(l.formats)
		if l.fixedOrder() {
			slices.SortStableFunc(formats, aimap.CompareFormats)
		}
	}
	if isMantleTarget(tm, providerType) {
		formats = slices.DeleteFunc(formats, func(f string) bool { return f == "bedrock" })
	}
	return formats
}

// formatCapabilities returns the capabilities of m for one format. For "*", it
// leaves out a gated capability and a WebSocket capability that the model
// cannot protect, with a warning.
func (c *Converter) formatCapabilities(
	src ConversionIssueSource, m *aigw.Model, l modelLists, format, providerType string,
) []string {
	if !l.anyCapability {
		caps := slices.Clone(l.capabilities)
		if l.fixedOrder() {
			slices.SortStableFunc(caps, aimap.CompareCapabilities)
		}
		return caps
	}
	var caps []string
	for _, capability := range aimap.WildcardCapabilities(format, providerType, m.Type) {
		field := capabilityField(m, capability)
		if f, gated := aimap.CapabilityFeature(capability); gated && !c.supports(f) {
			c.reportUnsupported(src.at(field), Warning, f, fmt.Sprintf("%s: %s", field, capability))
			continue
		}
		if needsWebSocket(format, capability, providerType) && !c.websocketProtectable(m) {
			c.warn(src.at(field), BehaviorMayDiffer,
				"model %q: capability %q needs a WebSocket route that the authentication of the "+
					"model cannot protect. This capability will be ignored.", m.Name, capability)
			continue
		}
		caps = append(caps, capability)
	}
	return caps
}

// servesPair reports whether a target of provider type providerType can serve
// capability in format.
func servesPair(format, capability, providerType string) bool {
	if _, ok := aimap.EndpointsFor(aimap.SectionFor(format, providerType), capability); !ok {
		return false
	}
	// Kong's schema rejects a passthrough-only capability on a provider that
	// renders another format.
	return !aimap.RequiresNativeFormat(capability) ||
		aimap.PluginProvider(providerType) == aimap.ClientFormat(format, providerType)
}

func needsWebSocket(format, capability, providerType string) bool {
	specs, _ := aimap.EndpointsFor(aimap.SectionFor(format, providerType), capability)
	return slices.ContainsFunc(specs, aimap.IsWebSocketEndpoint)
}

// websocketProtectable reports whether the authentication of m can protect a
// WebSocket route. convertModel fails an explicit WebSocket capability for
// the same conditions.
func (c *Converter) websocketProtectable(m *aigw.Model) bool {
	if c.globalAuthPolicy() != nil {
		return false
	}
	var refs []string
	for _, ref := range m.Access.AuthStrategies {
		if slices.Contains(refs, ref) {
			continue
		}
		refs = append(refs, ref)
		if idp := c.authStrategies[ref]; idp != nil && !aimap.SupportsWebSocket(idp.Type) {
			return false
		}
	}
	return len(refs) <= 1
}

// checkServedCapabilities fails m when no endpoint serves an explicit
// capability. A model with one format keeps the errors of a single format.
func (c *Converter) checkServedCapabilities(
	src ConversionIssueSource, m *aigw.Model, l modelLists, endpoints []modelEndpoint, providerType string,
) error {
	if l.anyCapability {
		return nil
	}
	for _, capability := range l.capabilities {
		if slices.ContainsFunc(endpoints, func(e modelEndpoint) bool { return e.capability == capability }) {
			continue
		}
		field := capabilityField(m, capability)
		if !l.anyFormat && len(l.formats) == 1 {
			format := aimap.ClientFormat(l.formats[0], providerType)
			if _, ok := aimap.EndpointsFor(aimap.SectionFor(format, providerType), capability); !ok {
				return c.fail(src.at(field), ConfigFieldInvalid,
					"model %q: capability %q is not supported with llm_format %q for provider type %q",
					m.Name, capability, format, providerType)
			}
			return c.fail(src.at(field), ConfigFieldInvalid,
				"model %q: capability %q is passthrough-only, so provider type %q cannot serve llm_format %q",
				m.Name, capability, providerType, format)
		}
		if f, gated := c.gatedFormatServing(l, capability, providerType); gated {
			return c.failUnsupported(src.at(field), f, fmt.Sprintf("%s: %s", field, capability))
		}
		return c.fail(src.at(field), ConfigFieldInvalid,
			"model %q: no format of the model serves capability %q for provider type %q",
			m.Name, capability, providerType)
	}
	return nil
}

// gatedFormatServing returns the gate of a "*" format that the minimum
// runtime version lacks and that serves capability.
func (c *Converter) gatedFormatServing(l modelLists, capability, providerType string) (aimap.Feature, bool) {
	if !l.anyFormat {
		return "", false
	}
	for _, format := range aimap.WildcardFormats() {
		if f, gated := aimap.FormatFeature(format); gated && !c.supports(f) &&
			servesPair(format, capability, providerType) {
			return f, true
		}
	}
	return "", false
}

// dropEqualPaths leaves out each endpoint that has the path and protocols of
// an endpoint of an earlier format. Kong cannot select between such routes.
func (c *Converter) dropEqualPaths(
	src ConversionIssueSource, m *aigw.Model, l modelLists, endpoints []modelEndpoint,
) []modelEndpoint {
	owners := map[string]string{}
	out := make([]modelEndpoint, 0, len(endpoints))
	for _, e := range endpoints {
		var specs []aimap.EndpointSpec
		for _, spec := range e.specs {
			key := fmt.Sprintf("%t|%t|%s", aimap.IsWebSocketEndpoint(spec), spec.IsRegex, spec.PathSuffix)
			if owner, ok := owners[key]; ok && owner != e.format {
				// With "*", the user cannot remove this warning.
				if !l.anyFormat && !l.anyCapability {
					c.warn(src.at(formatField(m, e.format)), RoutingConflict,
						"model %q: format %q serves capability %q on the same path as format %q. "+
							"The %q endpoint will be ignored.", m.Name, e.format, e.capability, owner, e.format)
				}
				continue
			}
			owners[key] = e.format
			specs = append(specs, spec)
		}
		if len(specs) > 0 {
			e.specs = specs
			out = append(out, e)
		}
	}
	return out
}

// servesOpenAIVideo reports whether m serves the video capability in the
// openai format. Only that endpoint gets a video lifecycle route.
func servesOpenAIVideo(m *aigw.Model) bool {
	if isPassthrough(m) {
		return false
	}
	l := effectiveLists(m)
	if !l.anyFormat && !slices.Contains(l.formats, videoLifecycleFormat) {
		return false
	}
	if l.anyCapability {
		return slices.Contains(aimap.WildcardCapabilities(videoLifecycleFormat, "", m.Type), "video")
	}
	return slices.Contains(l.capabilities, "video")
}

// isMantleTarget reports whether a bedrock target uses the mantle endpoint.
// That endpoint does not serve the Converse or InvokeModel APIs.
func isMantleTarget(tm *aigw.TargetModel, providerType string) bool {
	return providerType == "bedrock" && tm.Config.Options["endpoint_type"] == "mantle"
}

// onlyBedrockFormat reports whether every endpoint of m for a target of
// provider type providerType uses the bedrock format. A passthrough model uses
// the format of the provider.
func onlyBedrockFormat(m *aigw.Model, providerType string) bool {
	if isPassthrough(m) {
		return aimap.ClientFormat(aimap.PassthroughFormat, providerType) == "bedrock"
	}
	l := effectiveLists(m)
	return !l.anyFormat && slices.Equal(l.formats, []string{"bedrock"})
}

// isPassthrough reports whether a model forwards request bodies unchanged. The
// format is a whole-model property (convertModels rejects it alongside any other
// format), so every target of the model is passthrough or none is — which is
// also what ai-proxy-advanced requires of one plugin's targets.
func isPassthrough(m *aigw.Model) bool {
	l := effectiveLists(m)
	return !l.anyFormat && slices.Contains(l.formats, aimap.PassthroughFormat)
}
