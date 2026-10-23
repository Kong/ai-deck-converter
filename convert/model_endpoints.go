package convert

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// modelLists holds the effective formats and capabilities of a model. A
// wildcard list leaves its explicit values empty.
type modelLists struct {
	formats         []string
	formatsWildcard bool
	capabilities    []string
	capsWildcard    bool
}

func formatsWildcard(m *aigw.Model) bool {
	return slices.ContainsFunc(m.Formats, func(f aigw.Format) bool { return f.Type == aimap.Wildcard })
}

func capabilitiesWildcard(m *aigw.Model) bool {
	return slices.Contains(m.Capabilities, aimap.Wildcard)
}

// formatType returns the format of f. An empty type is the default format.
func formatType(f aigw.Format) string {
	if f.Type == "" {
		return aimap.DefaultLLMFormat
	}
	return f.Type
}

// explicitFormats returns the ordered formats of m, or nil when m uses "*".
func explicitFormats(m *aigw.Model) []string {
	if formatsWildcard(m) {
		return nil
	}
	var formats []string
	for _, f := range m.Formats {
		formats = append(formats, formatType(f))
	}
	if len(formats) == 0 {
		return []string{aimap.DefaultLLMFormat}
	}
	return aimap.OrderFormats(formats)
}

// explicitCapabilities returns the ordered canonical capabilities of m.
func explicitCapabilities(m *aigw.Model) []string {
	var out []string
	for _, capability := range m.Capabilities {
		out = append(out, aimap.NormalizeCapability(capability)...)
	}
	return aimap.OrderCapabilities(out)
}

// modelLists returns the effective lists of m. It fails m for a format that
// is not a value of aimap.Formats.
func (c *Converter) modelLists(src ConversionIssueSource, m *aigw.Model) (modelLists, error) {
	lists := modelLists{
		formatsWildcard: formatsWildcard(m),
		capsWildcard:    capabilitiesWildcard(m),
	}
	if !lists.formatsWildcard {
		valid := aimap.Formats()
		for i, f := range m.Formats {
			if f.Type != "" && !slices.Contains(valid, f.Type) {
				return lists, c.fail(src.at(fmt.Sprintf("formats[%d].type", i)), ConfigFieldInvalid,
					"model %q: format %q is not one of %s", m.Name, f.Type, strings.Join(valid, ", "))
			}
		}
		lists.formats = explicitFormats(m)
	}
	if !lists.capsWildcard {
		lists.capabilities = explicitCapabilities(m)
	}
	return lists, nil
}

// plannedTarget is a target that checkTarget accepts.
type plannedTarget struct {
	index        int
	tm           *aigw.TargetModel
	provider     *aigw.Provider
	providerType string
	target       aimap.Target
}

// aimapTarget returns the facts of tm that the data plane checks. mapOptions
// sends both format and llama2_format as llama2_format.
func aimapTarget(tm *aigw.TargetModel, providerType string) aimap.Target {
	opts := tm.Config.Options
	prefix, _ := opts["batch_bucket_prefix"].(string)
	return aimap.Target{
		ProviderType:      providerType,
		Mantle:            isMantleTarget(tm, providerType),
		BatchBucketPrefix: prefix != "",
		Llama2RawFormat:   providerType == "llama2" && (opts["format"] == "raw" || opts["llama2_format"] == "raw"),
	}
}

// effectiveFormats returns the explicit formats, or the formats that "*"
// selects for targets.
func (c *Converter) effectiveFormats(lists modelLists, targets []plannedTarget) []string {
	if !lists.formatsWildcard {
		return lists.formats
	}
	facts := make([]aimap.Target, len(targets))
	for i, t := range targets {
		facts[i] = t.target
	}
	var out []string
	for _, f := range aimap.SupportedFormats(facts, c.minRuntimeVersion) {
		out = append(out, f.Type)
	}
	return out
}

// endpointPair is one format and capability of a target, with the endpoints
// that the target supports.
type endpointPair struct {
	format     string
	capability string
	specs      []aimap.EndpointSpec
}

// targetPairs returns the ordered pairs of t that aimap.TargetSupports
// accepts. A pair whose capability has a version gate in its section is left
// out. checkModelCapabilities fails the model when no other pair is left.
func (c *Converter) targetPairs(m *aigw.Model, lists modelLists, formats []string, t plannedTarget) []endpointPair {
	var pairs []endpointPair
	for _, format := range formats {
		section := aimap.SectionFor(format, t.providerType)
		capabilities := lists.capabilities
		if lists.capsWildcard {
			capabilities = aimap.SupportedCapabilities(aigw.Format{Type: format}, t.target, m.Type, c.minRuntimeVersion)
		}
		for _, capability := range capabilities {
			specs, ok := aimap.EndpointsFor(section, capability)
			if !ok {
				continue
			}
			if f, gated := aimap.CapabilityFeature(section, capability); gated && !c.supports(f) {
				continue
			}
			var accepted []aimap.EndpointSpec
			for _, spec := range specs {
				if !aimap.TargetSupports(format, t.target, spec) {
					continue
				}
				if lists.capsWildcard && aimap.IsWebSocketEndpoint(spec) && !c.canProtectWebSocket(m) {
					continue
				}
				accepted = append(accepted, spec)
			}
			if len(accepted) > 0 {
				pairs = append(pairs, endpointPair{format: format, capability: capability, specs: accepted})
			}
		}
	}
	return pairs
}

// canProtectWebSocket reports whether the auth of m can protect a WebSocket
// route. A global auth policy keeps the http and https protocols. More than
// one auth strategy needs the anonymous fallback, which ws and wss lack.
func (c *Converter) canProtectWebSocket(m *aigw.Model) bool {
	if c.globalAuthPolicy() != nil {
		return false
	}
	return len(slices.Compact(slices.Sorted(slices.Values(m.Access.AuthStrategies)))) <= 1
}

// keepEqualPaths removes the endpoints whose path an earlier format in
// formats already serves for one of the targets. A model gets one route for
// each path.
func keepEqualPaths(formats []string, pairsByTarget map[int][]endpointPair) {
	pathKey := func(spec aimap.EndpointSpec) string {
		return spec.PathSuffix + "|" + strconv.FormatBool(spec.IsRegex)
	}
	owner := map[string]string{}
	for _, format := range formats {
		for _, pairs := range pairsByTarget {
			for _, pair := range pairs {
				if pair.format != format {
					continue
				}
				for _, spec := range pair.specs {
					if _, ok := owner[pathKey(spec)]; !ok {
						owner[pathKey(spec)] = format
					}
				}
			}
		}
	}
	for index, pairs := range pairsByTarget {
		var kept []endpointPair
		for _, pair := range pairs {
			pair.specs = slices.DeleteFunc(slices.Clone(pair.specs), func(spec aimap.EndpointSpec) bool {
				return owner[pathKey(spec)] != pair.format
			})
			if len(pair.specs) > 0 {
				kept = append(kept, pair)
			}
		}
		pairsByTarget[index] = kept
	}
}

// checkModelCapabilities fails m when the minimum runtime version lacks an
// explicit capability in every format that serves it.
func (c *Converter) checkModelCapabilities(src ConversionIssueSource, m *aigw.Model, lists modelLists) error {
	if lists.capsWildcard {
		return nil
	}
	formats := lists.formats
	if lists.formatsWildcard {
		formats = aimap.OrderFormats(slices.DeleteFunc(aimap.Formats(), func(f string) bool {
			return f == aimap.PassthroughFormat
		}))
	}
	for _, capability := range lists.capabilities {
		var gate aimap.Feature
		served := false
		for _, format := range formats {
			section := aimap.SectionFor(format, "")
			if _, ok := aimap.EndpointsFor(section, capability); !ok {
				continue
			}
			f, gated := aimap.CapabilityFeature(section, capability)
			if !gated || c.supports(f) {
				served = true
				break
			}
			gate = f
		}
		if served {
			continue
		}
		if gate == "" {
			// No format has an endpoint. The gate of the first format
			// still applies, so a gated capability fails as a gate.
			f, gated := aimap.CapabilityFeature(aimap.SectionFor(formats[0], ""), capability)
			if !gated || c.supports(f) {
				continue
			}
			gate = f
		}
		field := capabilityField(m, capability)
		return c.failUnsupported(src.at(field), gate, fmt.Sprintf("%s: %s", field, capability))
	}
	return nil
}

// checkCapabilityEndpoints fails m when no format has an endpoint for an
// explicit capability.
func (c *Converter) checkCapabilityEndpoints(
	src ConversionIssueSource, m *aigw.Model, lists modelLists, formats []string, providerType string,
) error {
	if lists.capsWildcard {
		return nil
	}
	for _, capability := range lists.capabilities {
		served := slices.ContainsFunc(formats, func(format string) bool {
			_, ok := aimap.EndpointsFor(aimap.SectionFor(format, providerType), capability)
			return ok
		})
		if !served {
			return c.fail(src.at(capabilityField(m, capability)), ConfigFieldInvalid,
				"model %q: capability %q is not supported with llm_format %q for provider type %q",
				m.Name, capability, strings.Join(formats, ", "), providerType)
		}
	}
	return nil
}

// warnUnservedFormats warns for each explicit format that gives no endpoint
// for any target.
func (c *Converter) warnUnservedFormats(
	src ConversionIssueSource, m *aigw.Model, lists modelLists, pairsByTarget map[int][]endpointPair,
) {
	if lists.formatsWildcard || len(pairsByTarget) == 0 {
		return
	}
	if !lists.capsWildcard && len(lists.capabilities) == 0 {
		return
	}
	served := map[string]bool{}
	for _, pairs := range pairsByTarget {
		for _, pair := range pairs {
			served[pair.format] = true
		}
	}
	for i, f := range m.Formats {
		format := formatType(f)
		if served[format] || slices.Contains(explicitFormatsBefore(m, i), format) {
			continue
		}
		c.warn(src.at(fmt.Sprintf("formats[%d].type", i)), ConfigFieldDropped,
			"model %q: no target supports an endpoint of format %q. "+
				"The converter makes no route for this format.",
			m.Name, format)
	}
}

// explicitFormatsBefore returns the formats of m before index i.
func explicitFormatsBefore(m *aigw.Model, i int) []string {
	var out []string
	for _, f := range m.Formats[:i] {
		out = append(out, formatType(f))
	}
	return out
}
