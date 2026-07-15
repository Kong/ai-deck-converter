// Package aimap holds the mapping tables shared by the forward converter
// (AI Gateway -> Kong decK) and the reverse converter (Kong decK -> AI
// Gateway): the endpoint table, capability normalization, provider enum
// mapping, option-key nesting sets, and label/tag conversion. Keeping both
// directions on one table guarantees they cannot drift.
package aimap

import (
	"sort"
	"strings"
)

// EndpointSpec describes the Kong route that serves a given (section, capability).
// Routes are grouped by (section, RouteLabel); specs sharing a label collapse to
// one route whose ai-proxy-advanced plugin carries one target per capability/model.
type EndpointSpec struct {
	RouteLabel     string   // route name suffix, e.g. "chat", "invoke"
	PathSuffix     string   // appended after the base path (regex body when IsRegex)
	IsRegex        bool     // emit a Kong regex route ("~" prefix)
	Methods        []string // route methods
	RouteType      string   // ai-proxy-advanced target route_type
	GenaiCategory  string   // ai-proxy-advanced config.genai_category
	TakesBodyModel bool     // whether requests carry a body `model` field (ai-model-selector)
}

const (
	catTextGen    = "text/generation"
	catEmbeddings = "text/embeddings"
	catImage      = "image/generation"
	catVideo      = "video/generation"
	catRealtime   = "realtime/generation"
	catSpeech     = "audio/speech"
	catTranscript = "audio/transcription"
)

// Shared defaults and the converged gateway service identity.
const (
	DefaultLLMFormat      = "openai"
	DefaultBasePath       = "/ai"
	DefaultMaxBodySize    = 8388608
	DefaultLogStatistics  = true
	DefaultLogPayloads    = false
	DefaultLogAudits      = false
	DefaultMaxPayloadSize = 1048576

	GatewayServiceName = "ai-gateway"
	GatewayServiceURL  = "http://ai-gateway.upstream.local"
)

var (
	mPost    = []string{"POST"}
	mGetPost = []string{"GET", "POST"}
)

// SectionFor selects the endpoint section from the model's llm_format (the
// client-facing wire format that determines the request paths). The only case
// where the provider type matters is gemini-format traffic served by Vertex,
// which uses Vertex's project/location URL templates instead of Gemini's.
func SectionFor(format, providerType string) string {
	if format == "" {
		format = DefaultLLMFormat
	}
	if format == "gemini" && providerType == "vertex" {
		return "vertex"
	}
	return format
}

// renderingSections are EndpointTable sections that are provider-specific renderings of a client
// format rather than formats in their own right: SectionFor routes some (format, providerType)
// pairs to them (the gemini format served by Vertex -> "vertex"). Each maps to its base format.
// They are excluded from Formats and folded into their base format's capabilities. Keep in step
// with SectionFor's special cases.
var renderingSections = map[string]string{
	"vertex": "gemini",
}

// Formats returns the client-facing wire formats a model may declare (the valid Format.Type
// values), sorted. Provider-rendering sections such as "vertex" are EndpointTable keys but not
// formats, so they are excluded.
func Formats() []string {
	out := make([]string, 0, len(EndpointTable))
	for section := range EndpointTable {
		if _, rendering := renderingSections[section]; rendering {
			continue
		}
		out = append(out, section)
	}
	sort.Strings(out)
	return out
}

// CapabilitiesFor returns the capabilities a model of the given client format may declare when
// served by the given provider type, resolved through the same section routing the converter uses
// (SectionFor) — so the gemini format served by Vertex reports the Vertex-only image, video, and
// rerank capabilities, while served by Gemini it does not. "generate" is listed first when
// present, the rest sorted. An unknown format, or a rendering section passed as a format, yields
// nil — keeping parity with Formats, which excludes those sections.
func CapabilitiesFor(format, providerType string) []string {
	if _, rendering := renderingSections[format]; rendering {
		return nil
	}
	caps, ok := EndpointTable[SectionFor(format, providerType)]
	if !ok {
		return nil
	}
	rest := make([]string, 0, len(caps))
	hasGenerate := false
	for c := range caps {
		if c == "generate" {
			hasGenerate = true
			continue
		}
		rest = append(rest, c)
	}
	sort.Strings(rest)
	out := make([]string, 0, len(caps))
	if hasGenerate {
		out = append(out, "generate")
	}
	return append(out, rest...)
}

// EndpointTable maps section -> capability -> endpoint spec, derived from
// ref/supported-endpoints.md and the reference kong.yaml examples.
var EndpointTable = map[string]map[string]EndpointSpec{
	"openai": {
		"generate":     {"chat", "/chat/completions", false, mPost, "llm/v1/chat", catTextGen, true},
		"agentic":      {"responses", "/responses", false, mPost, "llm/v1/responses", catTextGen, true},
		"realtime":     {"realtime", "/realtime", false, mGetPost, "realtime/v1/realtime", catRealtime, true},
		"embeddings":   {"embeddings", "/embeddings", false, mPost, "llm/v1/embeddings", catEmbeddings, true},
		"image":        {"images", "/images/generations", false, mPost, "image/v1/images/generations", catImage, true},
		"audio/speech": {"audio-speech", "/audio/speech", false, mPost, "audio/v1/audio/speech", catSpeech, true},
		"audio/transcription": {
			"audio-transcribe", "/audio/transcriptions", false, mPost, "audio/v1/audio/transcriptions",
			catTranscript, true,
		},
		"audio/translation": {
			"audio-translate", "/audio/translations", false, mPost, "audio/v1/audio/translations",
			catTranscript, true,
		},
		"video":   {"videos", "/videos/generations", false, mPost, "video/v1/videos/generations", catVideo, true},
		"batches": {"batches", "/batches", false, mGetPost, "llm/v1/batches", catTextGen, false},
		"files": {
			"files", "/files", false, []string{"GET", "POST", "DELETE"}, "llm/v1/files", catTextGen, false,
		},
	},
	"anthropic": {
		"generate": {"messages", "/v1/messages", false, mPost, "llm/v1/chat", catTextGen, true},
		"batches":  {"batches", "/v1/messages/batches", false, mGetPost, "llm/v1/batches", catTextGen, false},
	},
	"bedrock": {
		"generate": {
			"converse", "model/(?<model_name>[^/]+)/converse(?:-stream)?",
			true, mGetPost, "llm/v1/chat", catTextGen, false,
		},
		"agentic": {
			"retrieve", "model/(?<model_name>[^/]+)/retrieveAndGenerate(?:Stream)?",
			true, mGetPost, "llm/v1/chat", catTextGen, false,
		},
		"embeddings": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "llm/v1/embeddings", catEmbeddings, false,
		},
		"image": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "image/v1/images/generations", catImage, false,
		},
		"audio/speech": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "llm/v1/chat", catTextGen, false,
		},
		"video": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "video/v1/videos/generations", catVideo, false,
		},
		"rerank": {
			"rerank", "model/(?<model_name>[^/]+)/rerank",
			true, mGetPost, "llm/v1/chat", catTextGen, false,
		},
		"batches": {
			"batches", "model/(?<model_name>[^/]+)/async-invoke",
			true, mGetPost, "llm/v1/batches", catTextGen, false,
		},
	},
	"gemini": {
		"generate": {
			"generate", "v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			true, mGetPost, "llm/v1/chat", catTextGen, true,
		},
		"embeddings": {
			"embeddings", "v1beta/models/(?<model_name>[^:/]+):(?:embedContent|batchEmbedContent)",
			true, mGetPost, "llm/v1/embeddings", catEmbeddings, true,
		},
		"batches": {"batches", "v1beta/batches", false, mGetPost, "llm/v1/batches", catTextGen, false},
		"files":   {"files", "(?:upload/)?v1beta/files", true, mGetPost, "llm/v1/chat", catTextGen, false},
	},
	"vertex": {
		"generate": {
			"generate",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			true, mGetPost, "llm/v1/chat", catTextGen, true,
		},
		"embeddings": {
			"embeddings",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):(?:embedContent|batchEmbedContent)",
			true, mGetPost, "llm/v1/embeddings", catEmbeddings, true,
		},
		"image": {
			"predict-long-running",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):predictLongRunning",
			true, mGetPost, "image/v1/images/generations", catImage, false,
		},
		"video": {
			"predict-long-running",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):predictLongRunning",
			true, mGetPost, "video/v1/videos/generations", catVideo, false,
		},
		"rerank": {
			"ranking",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/rankingConfigs/" +
				"(?<ranking_config>[^:/]+):rank",
			true, mGetPost, "llm/v1/chat", catTextGen, false,
		},
		"batches": {
			"batches",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/batchPredictionJobs",
			true, mGetPost, "llm/v1/batches", catTextGen, false,
		},
	},
	"cohere": {
		"rerank": {"rerank", "/v2/rerank", false, mPost, "llm/v1/chat", catTextGen, false},
	},
	"huggingface": {
		"generate": {"generate", "/generate", false, mPost, "llm/v1/chat", catTextGen, true},
	},
}

// CapabilityAliases maps loose capability spellings to canonical keys.
var CapabilityAliases = map[string]string{
	"chat":  "generate",
	"batch": "batches",
}

// NormalizeCapability expands a source capability into one or more canonical
// capability keys. Bare "audio" fans out to speech/transcription/translation.
func NormalizeCapability(c string) []string {
	if c == "audio" {
		return []string{"audio/speech", "audio/transcription", "audio/translation"}
	}
	if canonical, ok := CapabilityAliases[c]; ok {
		return []string{canonical}
	}
	return []string{c}
}

// LookupEndpoint returns the endpoint spec for a section + canonical capability.
func LookupEndpoint(sec, capability string) (EndpointSpec, bool) {
	caps, ok := EndpointTable[sec]
	if !ok {
		return EndpointSpec{}, false
	}
	spec, ok := caps[capability]
	return spec, ok
}

// RoutePath builds the full route path for a spec under the given base path.
func RoutePath(base string, spec EndpointSpec) string {
	// A trailing slash on the base (e.g. a root base path of "/") would collide
	// with the leading slash of the suffix and produce an empty path segment
	// like "//chat/completions", which Kong rejects.
	base = strings.TrimRight(base, "/")
	if spec.IsRegex {
		return "~" + base + "/" + spec.PathSuffix
	}
	return base + spec.PathSuffix
}

// PluginProvider maps an AI Gateway provider type to the ai-proxy-advanced
// provider enum. Vertex is served through the gemini provider.
func PluginProvider(providerType string) string {
	if providerType == "vertex" {
		return "gemini"
	}
	return providerType
}

// The option-key sets below mirror the DP's closed model.options record for
// ai-proxy-advanced targets (kong/llm/schemas/init.lua model_options_schema,
// which composes the per-provider nested records from
// kong/llm/schemas/options.lua). Kong's "azure" and "anthropic" nested option
// records exist in options.lua but are not part of that target schema (they
// back an unrelated metadata/introspection schema instead) — only the flat
// azure_*/anthropic_version keys are valid for a target, so no nested key set
// exists for them here.

// GeminiOptionKeys are the sub-fields of model.options.gemini (the llm_gemini
// variant, which adds endpoint_id over the plain gemini record). Also used to
// validate the gcp_environment source keys that fold into that nested record.
var GeminiOptionKeys = map[string]bool{
	"location_id": true, "api_endpoint": true, "endpoint_id": true, "project_id": true,
}

// BedrockOptionKeys are target-config keys (AI Gateway's flat vocabulary, not
// the DP's own nested field names) that fold into model.options.bedrock.
var BedrockOptionKeys = map[string]bool{
	"region": true, "embeddings_normalize": true, "video_output_s3_uri": true,
	"batch_bucket_prefix": true, "batch_role_arn": true, "performance_config_latency": true,
}

// BedrockNestedOptionKeys are the sub-fields of model.options.bedrock itself
// (the DP's own field names, e.g. "aws_region" rather than BedrockOptionKeys'
// "region") — used to validate a target-config `bedrock: {...}` block written
// directly in the DP's shape instead of through the flat rename above.
var BedrockNestedOptionKeys = map[string]bool{
	"aws_region": true, "aws_assume_role_arn": true, "aws_role_session_name": true,
	"aws_sts_endpoint_url": true, "embeddings_normalize": true, "performance_config_latency": true,
	"video_output_s3_uri": true, "batch_bucket_prefix": true, "batch_role_arn": true,
}

// CohereOptionKeys are the sub-fields of model.options.cohere.
var CohereOptionKeys = map[string]bool{
	"api_version": true, "embedding_input_type": true, "wait_for_model": true,
}

// HuggingFaceOptionKeys are the sub-fields of model.options.huggingface.
var HuggingFaceOptionKeys = map[string]bool{
	"use_cache": true, "wait_for_model": true,
}

// DatabricksOptionKeys are the sub-fields of model.options.databricks.
var DatabricksOptionKeys = map[string]bool{
	"workspace_instance_id": true,
}

// DashscopeOptionKeys are the sub-fields of model.options.dashscope.
var DashscopeOptionKeys = map[string]bool{
	"international": true,
}

// KimiOptionKeys are the sub-fields of model.options.kimi.
var KimiOptionKeys = map[string]bool{
	"international": true,
}

// ModelOptionKeys are the flat scalar keys the DP's closed model.options record
// accepts as raw pass-through input. It deliberately excludes the per-provider
// nested record names (bedrock, gemini, cohere, ...): a raw opts key sharing a
// nested record's name is validated sub-field-by-sub-field against that
// record's own key set instead (see mapOptions), rather than accepted or
// dropped as a whole (AG-1246 follow-up).
var ModelOptionKeys = map[string]bool{
	"anthropic_version":     true,
	"azure_api_version":     true,
	"azure_deployment_id":   true,
	"azure_instance":        true,
	"embeddings_dimensions": true,
	"input_cost":            true,
	"llama2_format":         true,
	"max_tokens":            true,
	"mistral_format":        true,
	"output_cost":           true,
	"temperature":           true,
	"top_k":                 true,
	"top_p":                 true,
	"upstream_url":          true,
}
