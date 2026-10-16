package aimap

import (
	"cmp"
	"slices"
)

// Wildcard is the formats and capabilities value that selects every value.
const Wildcard = "*"

// apiCapabilities are the capabilities that do not route by model. Only a
// type "api" model serves them.
var apiCapabilities = map[string]bool{"batches": true, "files": true, "skills": true}

// IsAPICapability reports whether only a type "api" model serves capability.
func IsAPICapability(capability string) bool { return apiCapabilities[capability] }

// WildcardFormats returns the formats that "*" selects, in pair order.
// PassthroughFormat cannot be combined with other formats, so it is left out.
func WildcardFormats() []string {
	out := make([]string, 0, len(EndpointTable))
	for _, format := range Formats() {
		if format != PassthroughFormat {
			out = append(out, format)
		}
	}
	slices.SortStableFunc(out, CompareFormats)
	return out
}

// WildcardCapabilities returns the capabilities that "*" selects for one
// format, provider type, and model type, in pair order.
func WildcardCapabilities(format, providerType, modelType string) []string {
	apiModel := modelType == "api"
	var out []string
	for _, capability := range CapabilitiesFor(NormalizeFormat(format), providerType) {
		if IsAPICapability(capability) == apiModel {
			out = append(out, capability)
		}
	}
	slices.SortStableFunc(out, CompareCapabilities)
	return out
}

// CompareFormats orders formats in pair order. DefaultLLMFormat is first.
func CompareFormats(a, b string) int {
	return cmp.Or(cmp.Compare(rank(a, DefaultLLMFormat), rank(b, DefaultLLMFormat)), cmp.Compare(a, b))
}

// CompareCapabilities orders capabilities in pair order. "generate" is first.
// "realtime" is last, because the output lists the WebSocket Service after
// the HTTP Service. The reverter then recovers the capabilities in pair order.
func CompareCapabilities(a, b string) int {
	return cmp.Or(cmp.Compare(capabilityRank(a), capabilityRank(b)), cmp.Compare(a, b))
}

func capabilityRank(capability string) int {
	switch capability {
	case "generate":
		return 0
	case "realtime":
		return 2 //nolint:mnd
	default:
		return 1
	}
}

func rank(value, first string) int {
	if value == first {
		return 0
	}
	return 1
}
