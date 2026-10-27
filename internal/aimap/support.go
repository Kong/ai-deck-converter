package aimap

import (
	"slices"
	"sort"

	"github.com/Kong/ai-deck-converter/internal/aigw"
)

// Wildcard is the formats and capabilities value that selects every value.
const Wildcard = "*"

// Target holds the target facts that the data plane checks.
type Target struct {
	ProviderType string
	// Mantle is true when a bedrock target sets endpoint_type to mantle.
	Mantle bool
	// BatchBucketPrefix is true when a bedrock target sets
	// batch_bucket_prefix.
	BatchBucketPrefix bool
	// Llama2RawFormat is true when a llama2 target sets format to raw.
	Llama2RawFormat bool
	// GeminiEnterprise is true when a gemini target sets project_id and
	// location_id.
	GeminiEnterprise bool
}

// supportedRouteTypes copies SUPPORTED_ROUTE_TYPES of
// kong/llm/schemas/init.lua for AI Gateway 2.3. A route type with no row
// accepts every provider.
var supportedRouteTypes = map[string][]string{
	"llm/v1/chat": {
		"openai", "azure", "anthropic", "cohere", "mistral", "llama2", "gemini", "bedrock",
		"databricks", "huggingface", "dashscope", "xai", "cerebras", "vllm", "deepseek",
		"ollama", "kimi", "vercel", "sagemaker", "typesafe", "baseten",
	},
	"llm/v1/completions": {
		"openai", "azure", "anthropic", "cohere", "mistral", "llama2", "gemini", "bedrock",
		"huggingface", "xai", "ollama",
	},
	"llm/v1/embeddings": {
		"openai", "azure", "cohere", "mistral", "llama2", "gemini", "bedrock", "databricks",
		"huggingface", "dashscope", "ollama", "vercel",
	},
	"llm/v1/responses":              {"openai", "azure", "xai", "vercel"},
	"llm/v1/assistants":             {"openai", "azure"},
	"llm/v1/batches":                {"openai", "azure", "bedrock", "anthropic", "gemini", "kimi"},
	"llm/v1/files":                  {"openai", "azure", "kimi"},
	"llm/v1/skills":                 {"openai", "anthropic"},
	"image/v1/images/generations":   {"openai", "azure", "bedrock", "gemini", "dashscope", "xai", "vercel"},
	"image/v1/images/edits":         {"openai", "azure", "bedrock", "gemini", "dashscope"},
	"audio/v1/audio/speech":         {"openai", "azure"},
	"audio/v1/audio/translations":   {"openai", "azure"},
	"audio/v1/audio/transcriptions": {"openai", "azure"},
	RealtimeRouteType:               {"openai", "azure", "gemini"},
	"video/v1/videos/generations":   {"openai", "azure", "bedrock", "gemini", "huggingface"},
}

// unsupportedLogStatistics copies the union of UNSUPPORTED_LOG_STATISTICS in
// AI Gateway 2.0 to 2.3.
var unsupportedLogStatistics = map[string][]string{
	"llm/v1/completions":            {"anthropic"},
	"llm/v1/assistants":             {"openai", "azure"},
	"llm/v1/batches":                {"openai", "azure"},
	"llm/v1/skills":                 {"openai", "anthropic"},
	"image/v1/images/generations":   {"bedrock"},
	"image/v1/images/edits":         {"bedrock"},
	"audio/v1/audio/speech":         {"openai", "azure"},
	"audio/v1/audio/transcriptions": {"openai", "azure"},
	"audio/v1/audio/translations":   {"openai", "azure"},
}

// TargetSupports reports whether the data plane accepts target on spec in
// format.
func TargetSupports(format string, target Target, spec EndpointSpec) bool {
	provider := PluginProvider(target.ProviderType)
	routeType := spec.RouteType
	switch {
	case format == "openai", format == provider:
	case format == "anthropic" && routeType == "llm/v1/chat":
	default:
		return false
	}
	// The mantle list replaces the bedrock row of the route type table.
	if target.Mantle {
		if !MantleSupports(format, spec) {
			return false
		}
	} else if providers, ok := supportedRouteTypes[routeType]; ok && !slices.Contains(providers, provider) &&
		!GeminiInteractions(format, provider, routeType) {
		return false
	}
	if provider == "gemini" && routeType == RealtimeRouteType && format != "gemini" {
		return false
	}
	if provider == "gemini" && isGeminiInteractions(spec) &&
		isGeminiEnterpriseInteractions(spec) != target.GeminiEnterprise {
		return false
	}
	if provider == "bedrock" && routeType == "llm/v1/batches" && !target.BatchBucketPrefix {
		return false
	}
	if routeType == "llm/v1/skills" && (provider == "openai" || provider == "anthropic") && format != provider {
		return false
	}
	if target.Llama2RawFormat && routeType == "llm/v1/embeddings" {
		return false
	}
	return true
}

// GeminiInteractions reports whether the pair is the Gemini Interactions API.
// The 2.3 route type table has no gemini row for llm/v1/responses, but
// FeatureGeminiAgenticCapability serves it in the gemini format.
func GeminiInteractions(format, provider, routeType string) bool {
	return format == "gemini" && provider == "gemini" && routeType == "llm/v1/responses"
}

// LogStatisticsSupported reports whether the data plane accepts
// log_statistics on a target of providerType on spec.
func LogStatisticsSupported(providerType string, spec EndpointSpec) bool {
	if !spec.SupportsLogStatistics {
		return false
	}
	return !slices.Contains(unsupportedLogStatistics[spec.RouteType], PluginProvider(providerType))
}

// SupportsModelNameHeader reports whether the data plane accepts
// model_name_header on spec. These endpoints do not route by model.
func SupportsModelNameHeader(spec EndpointSpec) bool {
	switch spec.RouteType {
	case "llm/v1/assistants", "llm/v1/batches", "llm/v1/files", "llm/v1/skills":
		return false
	}
	return true
}

// OrderFormats returns the unique formats with openai first and the rest
// sorted. Routes follow this order, so the order of the source list does not
// change the output.
func OrderFormats(formats []string) []string {
	return orderFirst(formats, "openai")
}

// OrderCapabilities returns the unique capabilities with generate first and
// the rest sorted.
func OrderCapabilities(capabilities []string) []string {
	return orderFirst(capabilities, "generate")
}

func orderFirst(values []string, first string) []string {
	out := slices.Clone(values)
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i] == first) != (out[j] == first) {
			return out[i] == first
		}
		return out[i] < out[j]
	})
	return slices.Compact(out)
}

// SupportedFormats returns the formats represented by "*" for a given
// list of model targets and minRuntimeVersion.
func SupportedFormats(targets []Target, minRuntimeVersion Version) []aigw.Format {
	var formats []string
	for _, format := range Formats() {
		if format == PassthroughFormat {
			continue
		}
		if f, gated := FormatFeature(format); gated && !Supports(f, minRuntimeVersion) {
			continue
		}
		if slices.ContainsFunc(targets, func(t Target) bool { return formatServes(format, t) }) {
			formats = append(formats, format)
		}
	}
	out := make([]aigw.Format, 0, len(formats))
	for _, format := range OrderFormats(formats) {
		out = append(out, aigw.Format{Type: format})
	}
	return out
}

func formatServes(format string, target Target) bool {
	for _, ce := range SectionEndpoints(format) {
		if TargetSupports(format, target, ce.Spec) {
			return true
		}
	}
	return false
}

// SupportedCapabilities returns the capabilities represented by "*" for a given
// format, target, model type, and minimum runtime version.
func SupportedCapabilities(
	format aigw.Format, target Target, modelType string, minRuntimeVersion Version,
) []aigw.Capability {
	section := SectionFor(format.Type, target.ProviderType)
	// claimed holds the genai category of each route label. One
	// ai-proxy-advanced plugin serves one genai category.
	claimed := map[string]string{}
	var out []aigw.Capability
	for _, capability := range CapabilitiesFor(format.Type, target.ProviderType) {
		if (modelType == "api") != (capability == "batches" || capability == "files") {
			continue
		}
		if f, gated := CapabilityFeature(section, capability); gated && !Supports(f, minRuntimeVersion) {
			continue
		}
		specs, _ := EndpointsFor(section, capability)
		var labels []string
		for _, spec := range specs {
			if !TargetSupports(format.Type, target, spec) {
				continue
			}
			if category, ok := claimed[spec.RouteLabel]; ok && category != spec.GenaiCategory {
				continue
			}
			labels = append(labels, spec.RouteLabel)
		}
		if len(labels) == 0 {
			continue
		}
		for _, spec := range specs {
			if slices.Contains(labels, spec.RouteLabel) {
				claimed[spec.RouteLabel] = spec.GenaiCategory
			}
		}
		out = append(out, capability)
	}
	return out
}
