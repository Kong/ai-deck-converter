package convert

// endpointSpec describes the Kong route that serves a given (section, capability).
// Routes are grouped by (section, RouteLabel); specs sharing a label collapse to
// one route whose ai-proxy-advanced plugin carries one target per capability/model.
type endpointSpec struct {
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

var (
	mPost    = []string{"POST"}
	mGetPost = []string{"GET", "POST"}
)

// sectionFor selects the endpoint section from the model's llm_format (the
// client-facing wire format that determines the request paths). The only case
// where the provider type matters is gemini-format traffic served by Vertex,
// which uses Vertex's project/location URL templates instead of Gemini's.
func sectionFor(format, providerType string) string {
	if format == "" {
		format = defaultLLMFormat
	}
	if format == "gemini" && providerType == "vertex" {
		return "vertex"
	}
	return format
}

// endpointTable maps section -> capability -> endpoint spec, derived from
// ref/supported-endpoints.md and the reference kong.yaml examples.
var endpointTable = map[string]map[string]endpointSpec{
	"openai": {
		"generate":            {"chat", "/chat/completions", false, mPost, "llm/v1/chat", catTextGen, true},
		"agentic":             {"responses", "/responses", false, mPost, "llm/v1/responses", catTextGen, true},
		"realtime":            {"realtime", "/realtime", false, mGetPost, "realtime/v1/realtime", catRealtime, true},
		"embeddings":          {"embeddings", "/embeddings", false, mPost, "llm/v1/embeddings", catEmbeddings, true},
		"image":               {"images", "/images/generations", false, mPost, "image/v1/images/generations", catImage, true},
		"audio/speech":        {"audio-speech", "/audio/speech", false, mPost, "audio/v1/audio/speech", catSpeech, true},
		"audio/transcription": {"audio-transcribe", "/audio/transcriptions", false, mPost, "audio/v1/audio/transcriptions", catTranscript, true},
		"audio/translation":   {"audio-translate", "/audio/translations", false, mPost, "audio/v1/audio/translations", catTranscript, true},
		"video":               {"videos", "/videos/generations", false, mPost, "video/v1/videos/generations", catVideo, true},
		"batches":             {"batches", "/batches", false, mGetPost, "llm/v1/batches", catTextGen, false},
		"files":               {"files", "/files", false, []string{"GET", "POST", "DELETE"}, "llm/v1/files", catTextGen, false},
	},
	"anthropic": {
		"generate": {"messages", "/v1/messages", false, mPost, "llm/v1/chat", catTextGen, true},
		"batches":  {"batches", "/v1/messages/batches", false, mGetPost, "llm/v1/chat", catTextGen, false},
	},
	"bedrock": {
		"generate":     {"converse", "model/(?<model_name>[^/]+)/converse(?:-stream)?", true, mGetPost, "llm/v1/chat", catTextGen, false},
		"agentic":      {"retrieve", "model/(?<model_name>[^/]+)/retrieveAndGenerate(?:Stream)?", true, mGetPost, "llm/v1/responses", catTextGen, false},
		"embeddings":   {"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?", true, mGetPost, "llm/v1/embeddings", catEmbeddings, false},
		"image":        {"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?", true, mGetPost, "image/v1/images/generations", catImage, false},
		"audio/speech": {"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?", true, mGetPost, "audio/v1/audio/speech", catSpeech, false},
		"video":        {"invoke", "model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?", true, mGetPost, "video/v1/videos/generations", catVideo, false},
		"rerank":       {"rerank", "model/(?<model_name>[^/]+)/rerank", true, mGetPost, "llm/v1/chat", catTextGen, false},
		"batches":      {"batches", "model/(?<model_name>[^/]+)/async-invoke", true, mGetPost, "llm/v1/batches", catTextGen, false},
	},
	"gemini": {
		"generate":   {"generate", "v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)", true, mGetPost, "llm/v1/chat", catTextGen, true},
		"embeddings": {"embeddings", "v1beta/models/(?<model_name>[^:/]+):(?:embedContent|batchEmbedContent)", true, mGetPost, "llm/v1/embeddings", catEmbeddings, true},
		"batches":    {"batches", "v1beta/batches", false, mGetPost, "llm/v1/batches", catTextGen, false},
		"files":      {"files", "(?:upload/)?v1beta/files", true, mGetPost, "llm/v1/files", catTextGen, false},
	},
	"vertex": {
		"generate":   {"generate", "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)", true, mGetPost, "llm/v1/chat", catTextGen, true},
		"embeddings": {"embeddings", "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):(?:embedContent|batchEmbedContent)", true, mGetPost, "llm/v1/embeddings", catEmbeddings, true},
		"image":      {"predict-long-running", "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):predictLongRunning", true, mGetPost, "image/v1/images/generations", catImage, false},
		"video":      {"predict-long-running", "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):predictLongRunning", true, mGetPost, "video/v1/videos/generations", catVideo, false},
		"rerank":     {"ranking", "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/rankingConfigs/(?<ranking_config>[^:/]+):rank", true, mGetPost, "llm/v1/chat", catTextGen, false},
		"batches":    {"batches", "v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/batchPredictionJobs", true, mGetPost, "llm/v1/batches", catTextGen, false},
	},
	"cohere": {
		"rerank": {"rerank", "/v2/rerank", false, mPost, "llm/v1/chat", catTextGen, false},
	},
	"huggingface": {
		"generate": {"generate", "/generate", false, mPost, "llm/v1/chat", catTextGen, true},
	},
}

// capabilityAliases maps loose capability spellings to canonical keys.
var capabilityAliases = map[string]string{
	"chat":  "generate",
	"batch": "batches",
}

// normalizeCapability expands a source capability into one or more canonical
// capability keys. Bare "audio" fans out to speech/transcription/translation.
func normalizeCapability(c string) []string {
	if c == "audio" {
		return []string{"audio/speech", "audio/transcription", "audio/translation"}
	}
	if canonical, ok := capabilityAliases[c]; ok {
		return []string{canonical}
	}
	return []string{c}
}

// lookupEndpoint returns the endpoint spec for a section + canonical capability.
func lookupEndpoint(sec, capability string) (endpointSpec, bool) {
	caps, ok := endpointTable[sec]
	if !ok {
		return endpointSpec{}, false
	}
	spec, ok := caps[capability]
	return spec, ok
}

// routePath builds the full route path for a spec under the given base path.
func routePath(base string, spec endpointSpec) string {
	if spec.IsRegex {
		return "~" + base + "/" + spec.PathSuffix
	}
	return base + spec.PathSuffix
}
