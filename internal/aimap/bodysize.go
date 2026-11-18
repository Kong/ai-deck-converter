package aimap

// ProviderMaxBodySize maps a provider type to the request size limit, in
// bytes, that the provider publishes.
var ProviderMaxBodySize = map[string]int{
	// Anthropic documents a 32 MB limit for the Messages API.
	"anthropic": 32_000_000, //nolint:mnd
	// Anthropic documents a 20 MB limit for Claude on Bedrock. The
	// InvokeModel body limit is 25 MB.
	"bedrock": 20_000_000, //nolint:mnd
	// The Gemini API documents 100 MB of inline data for one request.
	// Vertex AI allows 500 MiB, but both use this provider type.
	"gemini": 100_000_000, //nolint:mnd
	// OpenAI documents a 50 MB limit for all file inputs of one request.
	"openai": 50_000_000, //nolint:mnd
}

// DefaultModelBodySize returns the max_request_body_size of a model that sets
// none. It is the smallest published limit of the model's providers. It is
// false when there is no provider or a provider publishes no limit.
func DefaultModelBodySize(providerTypes []string) (int, bool) {
	size := 0
	for _, t := range providerTypes {
		limit, ok := ProviderMaxBodySize[t]
		if !ok {
			return 0, false
		}
		if size == 0 || limit < size {
			size = limit
		}
	}
	return size, size > 0
}
