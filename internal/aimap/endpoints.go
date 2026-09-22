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
	RouteLabel                 string          // route name suffix, e.g. "chat", "invoke"
	PathSuffix                 string          // appended after the base path (regex body when IsRegex)
	IsRegex                    bool            // emit a Kong regex route ("~" prefix)
	Methods                    []string        // route methods
	RouteType                  string          // ai-proxy-advanced target route_type
	GenaiCategory              string          // ai-proxy-advanced config.genai_category
	DefaultModelSelectorConfig *map[string]any // optional default configuration for the ai-model-selector plugin
	SupportsLogStatistics      bool            // whether the endpoint supports log statistics
}

// EndpointEntry is EndpointTable's value: every Kong route that serves a given
// (section, capability). Primary is the canonical endpoint; Secondary holds
// extra specs for the rare capability reachable through more than one
// endpoint (bedrock "generate" is also served by InvokeModel, besides
// Converse). Read an entry through EndpointsFor or SectionEndpoints rather
// than indexing EndpointTable's fields directly, so callers that only expect
// one endpoint don't silently ignore a Secondary one.
type EndpointEntry struct {
	Primary   EndpointSpec
	Secondary []EndpointSpec
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
	DefaultBasePath       = "/"
	DefaultMaxBodySize    = 8388608
	DefaultLogStatistics  = true
	DefaultLogPayloads    = false
	DefaultLogAudits      = false
	DefaultMaxPayloadSize = 1048576

	GatewayServiceName = "ai-gateway"
	GatewayServiceURL  = "http://ai-gateway.upstream.local"
	// VideoLifecycleRouteTag marks the generated companion route for ID-only
	// video operations. It is not a source AI Gateway route and is ignored by
	// the reverse converter.
	VideoLifecycleRouteTag = "aigw:video-lifecycle"
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
	format = NormalizeFormat(format)
	if format == "" {
		format = DefaultLLMFormat
	}
	if format == "gemini" && providerType == "vertex" {
		return "vertex"
	}

	return format
}

// NormalizeFormat maps a provider-rendering section named directly as a
// model's format (e.g. "vertex") to its base client-facing wire format
// ("gemini"). Vertex serves the same Gemini request/response shape, so a
// model that names its format "vertex" is equivalent to one that names
// "gemini" and is served by a vertex provider; keeping both spellings
// working here means llm_format/routing never fork on which one was used.
func NormalizeFormat(format string) string {
	if base, ok := renderingSections[format]; ok {
		return base
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

// CapabilityLabel returns the user-facing name for a canonical capability.
func CapabilityLabel(capability string) string {
	switch capability {
	case "agentic":
		return "Responses"
	case "generate":
		return "Chat completions"
	default:
		return capability
	}
}

var defaultBodyModelSelectorConfig = map[string]any{
	"source":    "body",
	"body_path": "model",
}

// Default path-selector patterns emitted by the forward converter for the
// path-based sections.
const (
	OpenAIDefaultPathPattern  = "model/([^/]+)/"
	GeminiDefaultPathPattern  = "models/([%w%.%-]+):"
	BedrockDefaultPathPattern = "model/([^/]+)/"
)

var geminiPathModelSelectorConfig = map[string]any{
	"source":       "path",
	"path_pattern": GeminiDefaultPathPattern,
}

var bedrockPathModelSelectorConfig = map[string]any{
	"source":       "path",
	"path_pattern": BedrockDefaultPathPattern,
}

// bedrockInvokeChatSpec is bedrock's InvokeModel endpoint as seen by its
// llm/v1/chat-typed capabilities: audio/speech's only endpoint, and the
// endpoint "generate" is also reachable through besides Converse. Defined
// once so the two EndpointTable entries that use it can never drift.
var bedrockInvokeChatSpec = EndpointSpec{
	"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
	true, mGetPost, "llm/v1/chat", catTextGen, &bedrockPathModelSelectorConfig, true,
}

// EndpointTable maps section -> capability -> EndpointEntry, derived from
// ref/supported-endpoints.md and the reference kong.yaml examples. Almost
// every entry sets only Primary; Secondary is for the rare capability served
// by more than one endpoint (bedrock "generate").
var EndpointTable = map[string]map[string]EndpointEntry{
	"openai": {
		"generate": {
			Primary: EndpointSpec{
				"chat", "/chat/completions", false, mPost, "llm/v1/chat", catTextGen,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"agentic": {
			Primary: EndpointSpec{
				"responses", "/responses", false, mPost, "llm/v1/responses", catTextGen,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"realtime": {
			Primary: EndpointSpec{
				"realtime", "/realtime", false, mGetPost, "realtime/v1/realtime", catRealtime,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"embeddings": {
			Primary: EndpointSpec{
				"embeddings", "/embeddings", false, mPost, "llm/v1/embeddings", catEmbeddings,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"image": {
			Primary: EndpointSpec{
				"images", "/images/generations", false, mPost, "image/v1/images/generations", catImage,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"audio/speech": {
			Primary: EndpointSpec{
				"audio-speech", "/audio/speech", false, mPost, "audio/v1/audio/speech", catSpeech,
				&defaultBodyModelSelectorConfig, false,
			},
		},
		"audio/transcription": {
			Primary: EndpointSpec{
				"audio-transcribe", "/audio/transcriptions", false, mPost, "audio/v1/audio/transcriptions",
				catTranscript, &defaultBodyModelSelectorConfig, false,
			},
		},
		"audio/translation": {
			Primary: EndpointSpec{
				"audio-translate", "/audio/translations", false, mPost, "audio/v1/audio/translations",
				catTranscript, &defaultBodyModelSelectorConfig, false,
			},
		},
		"video": {
			Primary: EndpointSpec{
				"videos", "/videos", false, mPost, "video/v1/videos/generations", catVideo,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"batches": {
			Primary: EndpointSpec{"batches", "/batches", false, mGetPost, "llm/v1/batches", catTextGen, nil, false},
		},
		"files": {
			Primary: EndpointSpec{
				"files", "/files", false, []string{"GET", "POST", "DELETE"}, "llm/v1/files", catTextGen, nil, true,
			},
		},
	},
	"anthropic": {
		"generate": {
			Primary: EndpointSpec{
				"messages", "/v1/messages", false, mPost, "llm/v1/chat", catTextGen,
				&defaultBodyModelSelectorConfig, true,
			},
		},
		"batches": {
			Primary: EndpointSpec{
				"batches", "/v1/messages/batches", false, mGetPost, "llm/v1/batches", catTextGen, nil, false,
			},
		},
	},
	"bedrock": {
		"generate": {
			Primary: EndpointSpec{
				"converse", "model/(?<model_name>[^/]+)/converse(?:-stream)?",
				true, mGetPost, "llm/v1/chat", catTextGen, &bedrockPathModelSelectorConfig, true,
			},
			// Also reachable through InvokeModel — the same endpoint
			// audio/speech uses — so a generate-capable model gets both
			// routes. Reusing the spec verbatim means the two capabilities
			// are indistinguishable on revert when a route/target carries no
			// other signal (see convert/testdata/58_bedrock_generate_and_speech).
			Secondary: []EndpointSpec{bedrockInvokeChatSpec},
		},
		"agentic": {
			Primary: EndpointSpec{
				"retrieve", "model/(?<model_name>[^/]+)/retrieveAndGenerate(?:Stream)?",
				true, mGetPost, "llm/v1/chat", catTextGen, &bedrockPathModelSelectorConfig, true,
			},
		},
		"embeddings": {
			Primary: EndpointSpec{
				"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
				true, mGetPost, "llm/v1/embeddings", catEmbeddings, &bedrockPathModelSelectorConfig, true,
			},
		},
		"image": {
			Primary: EndpointSpec{
				"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
				true, mGetPost, "image/v1/images/generations", catImage, &bedrockPathModelSelectorConfig, false,
			},
		},
		"audio/speech": {Primary: bedrockInvokeChatSpec},
		"video": {
			Primary: EndpointSpec{
				"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?",
				true, mGetPost, "video/v1/videos/generations", catVideo, &bedrockPathModelSelectorConfig, true,
			},
		},
		"rerank": {
			Primary: EndpointSpec{
				"rerank", "model/(?<model_name>[^/]+)/rerank",
				true, mGetPost, "llm/v1/chat", catTextGen, &bedrockPathModelSelectorConfig, true,
			},
		},
		// Bedrock batch inference is the model-invocation-job lifecycle API, not
		// async-invoke (async-invoke is single-request asynchronous inference,
		// which the DP classifies as video generation). These paths carry no
		// model, so the route is route-only with no ai-model-selector.
		"batches": {
			Primary: EndpointSpec{
				"batches", "model-invocation-jobs?(?:/[^/]+(?:/stop)?)?",
				true, mGetPost, "llm/v1/batches", catTextGen, nil, true,
			},
		},
	},
	"gemini": {
		"generate": {
			Primary: EndpointSpec{
				"generate", "v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
				true, mGetPost, "llm/v1/chat", catTextGen, &geminiPathModelSelectorConfig, true,
			},
		},
		"embeddings": {
			Primary: EndpointSpec{
				"embeddings", "v1beta/models/(?<model_name>[^:/]+):(?:embedContent|batchEmbedContents)",
				true, mGetPost, "llm/v1/embeddings", catEmbeddings, &geminiPathModelSelectorConfig, true,
			},
		},
		"batches": {
			Primary: EndpointSpec{"batches", "/v1beta/batches", false, mGetPost, "llm/v1/batches", catTextGen, nil, true},
		},
		"files": {
			Primary: EndpointSpec{"files", "(?:upload/)?v1beta/files", true, mGetPost, "llm/v1/chat", catTextGen, nil, true},
		},
	},
	"vertex": {
		"generate": {
			Primary: EndpointSpec{
				"generate",
				"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
					"(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
				true, mGetPost, "llm/v1/chat", catTextGen, &geminiPathModelSelectorConfig, true,
			},
		},
		"embeddings": {
			Primary: EndpointSpec{
				"embeddings",
				"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
					"(?<model_name>[^:/]+):(?:embedContent|batchEmbedContents)",
				true, mGetPost, "llm/v1/embeddings", catEmbeddings, &geminiPathModelSelectorConfig, true,
			},
		},
		"image": {
			Primary: EndpointSpec{
				"predict-long-running",
				"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
					"(?<model_name>[^:/]+):predictLongRunning",
				true, mGetPost, "image/v1/images/generations", catImage, &geminiPathModelSelectorConfig, true,
			},
		},
		"video": {
			Primary: EndpointSpec{
				"predict-long-running",
				"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
					"(?<model_name>[^:/]+):predictLongRunning",
				true, mGetPost, "video/v1/videos/generations", catVideo, &geminiPathModelSelectorConfig, true,
			},
		},
		"rerank": {
			Primary: EndpointSpec{
				"ranking",
				"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/rankingConfigs/" +
					"(?<ranking_config>[^:/]+):rank",
				true, mGetPost, "llm/v1/chat", catTextGen, nil, true,
			},
		},
		"batches": {
			Primary: EndpointSpec{
				"batches",
				"v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/batchPredictionJobs",
				true, mGetPost, "llm/v1/batches", catTextGen, nil, true,
			},
		},
	},
	"cohere": {
		"rerank": {
			Primary: EndpointSpec{
				"rerank", "/v2/rerank", false, mPost, "llm/v1/chat", catTextGen, &defaultBodyModelSelectorConfig, true,
			},
		},
	},
	"huggingface": {
		"generate": {
			Primary: EndpointSpec{
				"generate", "/generate", false, mPost, "llm/v1/chat", catTextGen, &defaultBodyModelSelectorConfig, true,
			},
		},
	},
	// typesafe models are served through TypeSafe's own built-in path routing:
	// the decisions endpoint lives at a fixed suffix under the model's base
	// path, and the route is regex-marked so TypeSafe's backend can continue
	// matching sub-paths of it.
	"typesafe": {
		"decisions": {
			Primary: EndpointSpec{
				"decisions", "v1/systemone", true, mPost, "llm/v1/chat", catTextGen,
				&defaultBodyModelSelectorConfig, true,
			},
		},
	},
}

// EndpointsFor returns every endpoint spec that serves a (section, capability)
// pair: the entry's Primary spec plus any Secondary specs, primary first. ok
// is false when the section/capability combination is unsupported.
func EndpointsFor(section, capability string) (specs []EndpointSpec, ok bool) {
	entry, ok := EndpointTable[section][capability]
	if !ok {
		return nil, false
	}
	specs = append(specs, entry.Primary)
	specs = append(specs, entry.Secondary...)
	return specs, true
}

// CapabilityEndpoint pairs a capability with one of the endpoint specs that serve it.
type CapabilityEndpoint struct {
	Capability string
	Spec       EndpointSpec
}

// SectionEndpoints returns every (capability, spec) pair served in a section —
// each entry's Primary spec plus every Secondary spec — for callers that need
// to scan a whole section (e.g. revert's endpoint resolution, which must
// consider secondary specs as candidates too).
func SectionEndpoints(section string) []CapabilityEndpoint {
	entries := EndpointTable[section]
	out := make([]CapabilityEndpoint, 0, len(entries))
	for capability, entry := range entries {
		out = append(out, CapabilityEndpoint{capability, entry.Primary})
		for _, spec := range entry.Secondary {
			out = append(out, CapabilityEndpoint{capability, spec})
		}
	}
	return out
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

// LookupEndpoint returns the primary endpoint spec for a section + canonical capability.
func LookupEndpoint(sec, capability string) (EndpointSpec, bool) {
	caps, ok := EndpointTable[sec]
	if !ok {
		return EndpointSpec{}, false
	}
	entry, ok := caps[capability]
	return entry.Primary, ok
}

// RoutePath builds the full route path for a spec under the given base path.
func RoutePath(base string, spec EndpointSpec) string {
	// A trailing slash on the base (e.g. a root base path of "/") would collide
	// with the leading slash of the suffix and produce an empty path segment
	// like "//chat/completions", which Kong rejects.
	base = strings.TrimRight(base, "/")

	// A model base may already be a regex path ("~^/deployments/(?<id>[^/]+)$").
	// Strip the single regex marker so we never emit an invalid "~~/..." route,
	// and drop a trailing "$" end-anchor: the format-specific suffix is appended
	// after the base, so keeping the anchor would strand a "$" mid-path and
	// reject the route.
	baseIsRegex := strings.HasPrefix(base, "~")
	base = strings.TrimPrefix(base, "~")

	// The route is a regex whenever either side contributes one: a regex base
	// needs the marker so Kong compiles its capture groups, a regex spec brings
	// its own capture in the suffix. Either way the anchor must go so the suffix
	// lands after it, and the single marker is re-added once.
	isRegex := baseIsRegex || spec.IsRegex
	if isRegex {
		base = trimRegexEndAnchor(base)
	}
	suffix := spec.PathSuffix
	if spec.IsRegex {
		// A regex suffix carries no leading slash of its own.
		suffix = "/" + suffix
	}
	if isRegex {
		return "~" + base + suffix
	}
	return base + suffix
}

// trimRegexEndAnchor drops a single trailing "$" end-anchor from a regex body so
// the format-specific suffix can be appended after it. A "$" escaped as "\$" is a
// literal dollar, not an anchor, and is preserved: an anchor is only recognized
// when preceded by an even number (including zero) of backslashes.
func trimRegexEndAnchor(s string) string {
	body, ok := strings.CutSuffix(s, "$")
	if !ok {
		return s
	}
	backslashes := 0
	for i := len(body) - 1; i >= 0 && body[i] == '\\'; i-- {
		backslashes++
	}
	if backslashes%2 == 1 {
		return s
	}
	return body
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
