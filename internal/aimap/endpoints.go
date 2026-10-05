// Package aimap holds the mapping tables shared by the forward converter
// (AI Gateway -> Kong decK) and the reverse converter (Kong decK -> AI
// Gateway): the endpoint table, capability normalization, provider enum
// mapping, option-key nesting sets, and label/tag conversion. Keeping both
// directions on one table guarantees they cannot drift.
package aimap

import (
	"slices"
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
	// PassthroughFormat is the model format that forwards the client's request
	// body to the provider unchanged. It is deliberately not an EndpointTable
	// section: it borrows the provider's own section (see ClientFormat) and
	// changes nothing but the target's route_type.
	PassthroughFormat = "passthrough"

	// PassthroughRouteType is the ai-proxy-advanced target route_type a
	// PassthroughFormat model emits. It requires AI Gateway 2.2 or later.
	PassthroughRouteType = "passthrough"

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

	// GatewayWebSocketServiceName names the Service for realtime routes.
	// Kong selects the WebSocket proxy path from the Service protocol.
	GatewayWebSocketServiceName = "ai-gateway-websocket"
	GatewayWebSocketServiceURL  = "ws://ai-gateway.upstream.local"

	// RealtimeRouteType is the route_type of the WebSocket realtime endpoint.
	RealtimeRouteType = "realtime/v1/realtime"
)

var (
	mPost          = []string{"POST"}
	mGetPost       = []string{"GET", "POST"}
	mGetPostDelete = []string{"GET", "POST", "DELETE"}
)

// IsWebSocketEndpoint reports whether spec serves WebSocket traffic.
// Kong rejects methods on its routes. ai-model-selector does not run on it.
func IsWebSocketEndpoint(spec EndpointSpec) bool {
	return spec.RouteType == RealtimeRouteType
}

// SectionFor selects the endpoint section from the model's llm_format (the
// client-facing wire format that determines the request paths). The provider
// type only matters for passthrough, which has no client format of its own
// (see ClientFormat). Gemini-format traffic served by a vertex provider uses
// the gemini section, which carries Gemini Enterprise's project/location URL
// templates as well.
func SectionFor(format, providerType string) string {
	return ClientFormat(format, providerType)
}

// ClientFormat returns the client-facing wire format a model renders as, which
// is the llm_format the ai-proxy-advanced plugin carries.
//
// It is the declared format for every format but PassthroughFormat. Passthrough
// forwards the client's request body unchanged, so the wire format is whatever
// the provider itself speaks natively rather than anything the model declares:
// the paths, methods and llm_format all come from the provider's own section,
// and only the target's route_type says passthrough.
func ClientFormat(format, providerType string) string {
	format = NormalizeFormat(format)
	if format == PassthroughFormat {
		format = NormalizeFormat(providerType)
		if !HasNativeFormat(providerType) {
			// Providers that speak no section of their own (azure, mistral,
			// databricks, ...) all expose OpenAI-shaped APIs.
			format = DefaultLLMFormat
		}
	}
	if format == "" {
		format = DefaultLLMFormat
	}

	return format
}

// HasNativeFormat reports whether a provider type speaks a wire format of its own, i.e. has a
// matching llm_format. Passthrough extracts usage with that format's adapter; any other provider
// falls back to OpenAI-shaped extraction, which may find nothing.
func HasNativeFormat(providerType string) bool {
	_, served := EndpointTable[NormalizeFormat(providerType)]
	return served
}

// NormalizeFormat maps a provider name used directly as a model's format
// (e.g. "vertex") to its base client-facing wire format ("gemini"). Vertex
// serves the same Gemini request/response shape, so a model that names its
// format "vertex" is equivalent to one that names "gemini"; keeping both
// spellings working here means llm_format/routing never fork on which one
// was used.
func NormalizeFormat(format string) string {
	if base, ok := formatAliases[format]; ok {
		return base
	}
	return format
}

// formatAliases are provider names accepted as a model's format that are not formats in their
// own right. Each maps to its base format. They are excluded from Formats.
var formatAliases = map[string]string{
	"vertex": "gemini",
}

// PromptReadingPolicies are the AI policies that parse the request or response into the
// normalized LLM shape before acting on it, so they may not work properly for a passthrough
// model: the body reaches the provider exactly as the client sent it, in whatever shape that
// provider speaks. ai-sanitizer belongs here only when it anonymizes credentials (see
// SanitizerAnonymizesCredentials); the other AI policies work on raw bytes.
var PromptReadingPolicies = map[string]bool{
	"ai-aws-guardrails":          true,
	"ai-azure-content-safety":    true,
	"ai-custom-guardrail":        true,
	"ai-gcp-model-armor":         true,
	"ai-lakera-guard":            true,
	"ai-llm-as-judge":            true,
	"ai-nvidia-nemo-guardrail":   true,
	"ai-prompt-compressor":       true,
	"ai-prompt-decorator":        true,
	"ai-prompt-guard":            true,
	"ai-prompt-template":         true,
	"ai-rag-injector":            true,
	"ai-semantic-cache":          true,
	"ai-semantic-prompt-guard":   true,
	"ai-semantic-response-guard": true,
}

// SanitizerAnonymizesCredentials reports whether an ai-sanitizer config anonymizes credentials,
// the one sanitizer mode that needs the normalized LLM shape. An unset anonymize defaults to
// all_and_credentials on the data plane. A list holding "all" as well is still reported: the data
// plane collapsing it to "all" is a bug tracked in KOKO-4587.
func SanitizerAnonymizesCredentials(cfg map[string]any) bool {
	var types []string
	switch v := cfg["anonymize"].(type) {
	case []string:
		types = v
	case []any:
		for _, t := range v {
			if s, ok := t.(string); ok {
				types = append(types, s)
			}
		}
	}
	if len(types) == 0 {
		return true
	}
	return slices.Contains(types, "all_and_credentials") || slices.Contains(types, "credentials")
}

// PassthroughEndpoint is the one route a PassthroughFormat model serves. Capabilities do not
// apply to it: every request under the model's base path is forwarded as it stands, whatever
// endpoint it names, so the route matches the base path itself on every method.
var PassthroughEndpoint = EndpointSpec{
	RouteLabel:            "passthrough",
	RouteType:             PassthroughRouteType,
	GenaiCategory:         catTextGen,
	SupportsLogStatistics: true,
}

// Formats returns the client-facing wire formats a model may declare (the valid Format.Type
// values), sorted: every EndpointTable section, plus PassthroughFormat, a valid format that is
// not a section. Format aliases such as "vertex" are not listed.
func Formats() []string {
	out := make([]string, 0, len(EndpointTable)+1)
	for section := range EndpointTable {
		out = append(out, section)
	}
	out = append(out, PassthroughFormat)
	sort.Strings(out)
	return out
}

// nativeFormatCapabilities are the capabilities Kong serves only as passthrough: unlike
// generate/image/... there is no request/response conversion between wire formats for them, so the
// serving provider has to render the model's own format (openai model on an openai provider,
// anthropic on anthropic). ai-proxy-advanced's schema rejects every other pairing.
var nativeFormatCapabilities = map[string]bool{"skills": true}

// RequiresNativeFormat reports whether capability is passthrough-only. Callers enforce it on the
// provider enum ai-proxy-advanced will carry, which is narrower than format rendering: Kong
// restricts these route types to the openai and anthropic provider enums, so an azure provider
// fails even though its traffic renders the openai format.
func RequiresNativeFormat(capability string) bool { return nativeFormatCapabilities[capability] }

// CapabilitiesFor returns the capabilities a model of the given client format may declare when
// served by the given provider type, resolved through the same section routing the converter uses
// (SectionFor). A passthrough-only capability is left out unless the provider renders the model's
// own format. "generate" is listed first when present, the rest sorted. An unknown format, or a
// format alias, yields nil — keeping parity with Formats, which excludes those aliases.
func CapabilitiesFor(format, providerType string) []string {
	if _, alias := formatAliases[format]; alias {
		return nil
	}
	section := SectionFor(format, providerType)
	caps, ok := EndpointTable[section]
	if !ok {
		return nil
	}
	provider := PluginProvider(providerType)
	rest := make([]string, 0, len(caps))
	hasGenerate := false
	for c := range caps {
		if c == "generate" {
			hasGenerate = true
			continue
		}
		// A passthrough-only capability is only reachable when the provider's own format is the
		// one the client speaks, which is exactly the section it was looked up in.
		if nativeFormatCapabilities[c] && section != provider {
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

// Gemini Enterprise URL templates the gemini section's Gemini Enterprise endpoints build on.
const (
	geminiEnterpriseLocPath   = "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)"
	geminiEnterpriseModelPath = geminiEnterpriseLocPath + "/publishers/(?<publisher>[^/]+)/models/(?<model_name>[^:/]+)"
)

// EndpointTable maps section -> capability -> EndpointEntry, derived from
// ref/supported-endpoints.md and the reference kong.yaml examples. Most
// entries set only Primary; Secondary is for capabilities served by more
// than one endpoint (bedrock "generate", gemini's enterprise endpoints).
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
				"realtime", "/realtime", false, nil, RealtimeRouteType, catRealtime,
				nil, true,
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
				"files", "/files", false, mGetPostDelete, "llm/v1/files", catTextGen, nil, true,
			},
		},
		"skills": {
			Primary: EndpointSpec{
				"skills", "/skills", false, mGetPostDelete, "llm/v1/skills", catTextGen, nil, false,
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
		"skills": {
			Primary: EndpointSpec{
				"skills", "/v1/skills", false, mGetPostDelete, "llm/v1/skills", catTextGen, nil, false,
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
	// The gemini section serves both the Gemini Standard API and Gemini Enterprise AI: every
	// capability either API offers is listed, with the Gemini Enterprise endpoint as a
	// Secondary spec where both do. Gemini Enterprise specs carry their own route labels
	// so the two path shapes never collapse into one route.
	"gemini": {
		"generate": {
			Primary: EndpointSpec{
				"generate", "v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
				true, mGetPost, "llm/v1/chat", catTextGen, &geminiPathModelSelectorConfig, true,
			},
			Secondary: []EndpointSpec{{
				"enterprise-generate", geminiEnterpriseModelPath + ":(?:generateContent|streamGenerateContent)",
				true, mGetPost, "llm/v1/chat", catTextGen, &geminiPathModelSelectorConfig, true,
			}},
		},
		"embeddings": {
			Primary: EndpointSpec{
				"embeddings", "v1beta/models/(?<model_name>[^:/]+):(?:embedContent|batchEmbedContents)",
				true, mGetPost, "llm/v1/embeddings", catEmbeddings, &geminiPathModelSelectorConfig, true,
			},
			Secondary: []EndpointSpec{{
				"enterprise-embeddings", geminiEnterpriseModelPath + ":embedContent",
				true, mGetPost, "llm/v1/embeddings", catEmbeddings, &geminiPathModelSelectorConfig, true,
			}},
		},
		"image": {
			Primary: EndpointSpec{
				"predict", geminiEnterpriseModelPath + ":predict",
				true, mGetPost, "image/v1/images/generations", catImage, &geminiPathModelSelectorConfig, true,
			},
		},
		"video": {
			Primary: EndpointSpec{
				"predict-long-running", geminiEnterpriseModelPath + ":predictLongRunning",
				true, mGetPost, "video/v1/videos/generations", catVideo, &geminiPathModelSelectorConfig, true,
			},
		},
		"rerank": {
			Primary: EndpointSpec{
				"ranking", geminiEnterpriseLocPath + "/rankingConfigs/(?<config_name>[^:/]+):rank",
				true, mGetPost, "llm/v1/chat", catTextGen, nil, true,
			},
		},
		"batches": {
			Primary: EndpointSpec{"batches", "/v1beta/batches", false, mGetPost, "llm/v1/batches", catTextGen, nil, true},
			Secondary: []EndpointSpec{{
				"enterprise-batches", geminiEnterpriseLocPath + "/batchPredictionJobs",
				true, mGetPost, "llm/v1/batches", catTextGen, nil, true,
			}},
		},
		"files": {
			Primary: EndpointSpec{"files", "(?:upload/)?v1beta/files", true, mGetPost, "llm/v1/chat", catTextGen, nil, true},
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
				"generate", "/v1/chat/completions", false, mPost, "llm/v1/chat", catTextGen, &defaultBodyModelSelectorConfig, true,
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
	Secondary  bool // Spec is one of the entry's Secondary specs, not its Primary
}

// SectionEndpoints returns every (capability, spec) pair served in a section —
// each entry's Primary spec plus every Secondary spec — for callers that need
// to scan a whole section (e.g. revert's endpoint resolution, which must
// consider secondary specs as candidates too).
func SectionEndpoints(section string) []CapabilityEndpoint {
	entries := EndpointTable[section]
	out := make([]CapabilityEndpoint, 0, len(entries))
	for capability, entry := range entries {
		out = append(out, CapabilityEndpoint{capability, entry.Primary, false})
		for _, spec := range entry.Secondary {
			out = append(out, CapabilityEndpoint{capability, spec, true})
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
	if base+suffix == "" {
		// A root base with no suffix (PassthroughEndpoint) is the root itself.
		return "/"
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
