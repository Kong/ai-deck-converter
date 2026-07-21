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
	RouteLabel            string   // route name suffix, e.g. "chat", "invoke"
	PathSuffix            string   // appended after the base path (regex body when IsRegex)
	IsRegex               bool     // emit a Kong regex route ("~" prefix)
	Methods               []string // route methods
	RouteType             string   // ai-proxy-advanced target route_type
	GenaiCategory         string   // ai-proxy-advanced config.genai_category
	TakesBodyModel        bool     // whether requests carry a body `model` field (ai-model-selector)
	SupportsLogStatistics bool     // whether the endpoint supports log statistics
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

// EndpointSectionFor selects the EndpointTable section that serves a single
// capability's route. It starts from SectionFor (which keeps a provider-specific
// rendering like Vertex distinct so capability enumeration is accurate) but, for
// such a rendering, prefers the base client format's section for any capability
// that format already serves. So gemini-format traffic served by Vertex renders
// generate/embeddings on Gemini's client paths (a Vertex backend is still
// reached via the gcp options and the gemini provider enum), while Vertex's
// exclusive image/video/rerank endpoints keep the Vertex project/location paths.
func EndpointSectionFor(format, providerType, capability string) string {
	sec := SectionFor(format, providerType)
	if base, ok := renderingSections[sec]; ok {
		// Only fall back when the rendering section supports this capability too,
		// otherwise we may accidentally enable base-only capabilities (e.g. files).
		if _, ok := LookupEndpoint(sec, capability); ok {
			if _, ok := LookupEndpoint(base, capability); ok {
				return base
			}
		}
	}
	return sec
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
		"generate":   {"chat", "/chat/completions", false, mPost, "llm/v1/chat", catTextGen, true, true},
		"agentic":    {"responses", "/responses", false, mPost, "llm/v1/responses", catTextGen, true, true},
		"realtime":   {"realtime", "/realtime", false, mGetPost, "realtime/v1/realtime", catRealtime, true, true},
		"embeddings": {"embeddings", "/embeddings", false, mPost, "llm/v1/embeddings", catEmbeddings, true, true},
		"image": {
			"images", "/images/generations", false, mPost, "image/v1/images/generations", catImage, true, true,
		},
		"audio/speech": {
			"audio-speech", "/audio/speech", false, mPost, "audio/v1/audio/speech", catSpeech, true, false,
		},
		"audio/transcription": {
			"audio-transcribe", "/audio/transcriptions", false, mPost, "audio/v1/audio/transcriptions",
			catTranscript, true, false,
		},
		"audio/translation": {
			"audio-translate", "/audio/translations", false, mPost, "audio/v1/audio/translations",
			catTranscript, true, false,
		},
		"video": {
			"videos", "/videos", false,
			[]string{"GET", "POST", "DELETE"},
			"video/v1/videos/generations",
			catVideo, true, true,
		},
		"batches": {"batches", "/batches", false, mGetPost, "llm/v1/batches", catTextGen, false, false},
		"files": {
			"files", "/files", false, []string{"GET", "POST", "DELETE"}, "llm/v1/files", catTextGen, false, true,
		},
	},
	"anthropic": {
		"generate": {"messages", "/v1/messages", false, mPost, "llm/v1/chat", catTextGen, true, true},
		"batches": {
			"batches", "/v1/messages/batches", false, mGetPost, "llm/v1/batches", catTextGen, false, false,
		},
	},
	"bedrock": {
		"generate": {
			"converse", "model/(?<model_name>[^/]+)/converse(?:-stream)?",
			true, mGetPost, "llm/v1/chat", catTextGen, false, true,
		},
		"agentic": {
			"retrieve", "model/(?<model_name>[^/]+)/retrieveAndGenerate(?:Stream)?",
			true, mGetPost, "llm/v1/chat", catTextGen, false, true,
		},
		"embeddings": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "llm/v1/embeddings", catEmbeddings, false, true,
		},
		"image": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "image/v1/images/generations", catImage, false, false,
		},
		"audio/speech": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "llm/v1/chat", catTextGen, false, true,
		},
		"video": {
			"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
			true, mGetPost, "video/v1/videos/generations", catVideo, false, true,
		},
		"rerank": {
			"rerank", "model/(?<model_name>[^/]+)/rerank",
			true, mGetPost, "llm/v1/chat", catTextGen, false, true,
		},
		"batches": {
			"batches", "model/(?<model_name>[^/]+)/async-invoke",
			true, mGetPost, "llm/v1/batches", catTextGen, false, true,
		},
	},
	"gemini": {
		"generate": {
			"generate", "v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			true, mGetPost, "llm/v1/chat", catTextGen, true, true,
		},
		"embeddings": {
			"embeddings", "v1beta/models/(?<model_name>[^:/]+):(?:embedContent|batchEmbedContent)",
			true, mGetPost, "llm/v1/embeddings", catEmbeddings, true, true,
		},
		"batches": {"batches", "v1beta/batches", false, mGetPost, "llm/v1/batches", catTextGen, false, true},
		"files":   {"files", "(?:upload/)?v1beta/files", true, mGetPost, "llm/v1/chat", catTextGen, false, true},
	},
	"vertex": {
		"generate": {
			"generate",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			true, mGetPost, "llm/v1/chat", catTextGen, true, true,
		},
		"embeddings": {
			"embeddings",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):(?:embedContent|batchEmbedContent)",
			true, mGetPost, "llm/v1/embeddings", catEmbeddings, true, true,
		},
		"image": {
			"predict-long-running",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):predictLongRunning",
			true, mGetPost, "image/v1/images/generations", catImage, false, true,
		},
		"video": {
			"predict-long-running",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):predictLongRunning",
			true, mGetPost, "video/v1/videos/generations", catVideo, false, true,
		},
		"rerank": {
			"ranking",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/rankingConfigs/" +
				"(?<ranking_config>[^:/]+):rank",
			true, mGetPost, "llm/v1/chat", catTextGen, false, true,
		},
		"batches": {
			"batches",
			"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/batchPredictionJobs",
			true, mGetPost, "llm/v1/batches", catTextGen, false, true,
		},
	},
	"cohere": {
		"rerank": {"rerank", "/v2/rerank", false, mPost, "llm/v1/chat", catTextGen, false, true},
	},
	"huggingface": {
		"generate": {"generate", "/generate", false, mPost, "llm/v1/chat", catTextGen, true, true},
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

// GeminiOptionKeys are target-config keys that nest under model.options.gemini
// for the gemini and vertex provider types.
var GeminiOptionKeys = map[string]bool{
	"location_id": true, "api_endpoint": true, "endpoint_id": true, "project_id": true,
}

// BedrockOptionKeys are target-config keys that nest under model.options.bedrock.
var BedrockOptionKeys = map[string]bool{
	"region": true, "embeddings_normalize": true, "video_output_s3_uri": true,
	"batch_bucket_prefix": true, "batch_role_arn": true, "performance_config_latency": true,
}
