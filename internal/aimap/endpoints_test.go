package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormats(t *testing.T) {
	got := Formats()
	// The valid Format.Type values, i.e. EndpointTable sections minus provider renderings.
	want := []string{"anthropic", "bedrock", "cohere", "gemini", "huggingface", "openai"}
	require.Equal(t, want, got)
	require.NotContains(t, got, "vertex", "vertex is a rendering of gemini, not a client format")
}

func TestCapabilitiesFor(t *testing.T) {
	// gemini served by Vertex folds in the Vertex-only image/video/rerank, generate first.
	require.Equal(t,
		[]string{"generate", "batches", "embeddings", "image", "rerank", "video"},
		CapabilitiesFor("gemini", "vertex"))
	// gemini served by Gemini has no image/video/rerank.
	require.Equal(t,
		[]string{"generate", "batches", "embeddings", "files"},
		CapabilitiesFor("gemini", "gemini"))
	// A format whose section is provider-independent.
	require.Equal(t, []string{"rerank"}, CapabilitiesFor("cohere", "cohere"))
	// An unknown format yields nil, as does a rendering section passed as a format (parity with
	// Formats, which excludes "vertex").
	require.Nil(t, CapabilitiesFor("nope", ""))
	require.Nil(t, CapabilitiesFor("vertex", "vertex"))
}

func TestSectionDerivation(t *testing.T) {
	cases := []struct {
		format, providerType, want string
	}{
		{"openai", "azure", "openai"},
		{"openai", "openai", "openai"},
		{"openai", "anthropic", "openai"}, // openai format translated to anthropic upstream
		{"anthropic", "anthropic", "anthropic"},
		{"bedrock", "bedrock", "bedrock"},
		{"gemini", "gemini", "gemini"},
		{"gemini", "vertex", "vertex"}, // gemini format served by Vertex
		{"", "openai", "openai"},       // default format
	}
	for _, tc := range cases {
		got := SectionFor(tc.format, tc.providerType)
		require.Equalf(t, tc.want, got, "SectionFor(%q,%q)", tc.format, tc.providerType)
	}
}

func TestEndpointSectionFor(t *testing.T) {
	cases := []struct {
		format, providerType, capability, want string
	}{
		// Vertex renders shared capabilities on Gemini's client paths...
		{"gemini", "vertex", "generate", "gemini"},
		{"gemini", "vertex", "embeddings", "gemini"},
		// ...but keeps the Vertex section for its exclusive capabilities.
		{"gemini", "vertex", "image", "vertex"},
		{"gemini", "vertex", "video", "vertex"},
		{"gemini", "vertex", "rerank", "vertex"},
		// Gemini served by Gemini is unaffected.
		{"gemini", "gemini", "generate", "gemini"},
		// Non-rendering sections pass through regardless of capability.
		{"openai", "openai", "generate", "openai"},
	}
	for _, tc := range cases {
		got := EndpointSectionFor(tc.format, tc.providerType, tc.capability)
		require.Equalf(t, tc.want, got,
			"EndpointSectionFor(%q,%q,%q)", tc.format, tc.providerType, tc.capability)
	}
}

func TestEndpointLookupAndNormalization(t *testing.T) {
	// chat -> generate -> openai chat completions.
	for _, capability := range NormalizeCapability("chat") {
		spec, ok := LookupEndpoint("openai", capability)
		require.True(t, ok, "openai chat lookup ok")
		require.Equal(t, "llm/v1/chat", spec.RouteType, "openai chat route type")
		require.Equal(t, "/chat/completions", spec.PathSuffix, "openai chat path suffix")
	}
	// bare audio fans out to three endpoints.
	require.Len(t, NormalizeCapability("audio"), 3, "NormalizeCapability(audio)")
	// batch alias.
	require.Equal(t, []string{"batches"}, NormalizeCapability("batch"), "NormalizeCapability(batch)")
	// unsupported (section,capability) returns not-ok.
	_, ok := LookupEndpoint("anthropic", "image")
	require.False(t, ok, "expected anthropic image to be unsupported")
}

func TestRoutePath(t *testing.T) {
	chat, _ := LookupEndpoint("openai", "generate")
	bedrock, _ := LookupEndpoint("bedrock", "generate")
	cases := []struct {
		name, base, want string
		spec             EndpointSpec
	}{
		{"plain base", "/ai", "/ai/chat/completions", chat},
		{"root base does not double slash", "/", "/chat/completions", chat},
		{"empty base", "", "/chat/completions", chat},
		{"trailing slash trimmed", "/ai/", "/ai/chat/completions", chat},
		{"regex plain base", "/ai", "~/ai/model/(?<model_name>[^/]+)/converse(?:-stream)?", bedrock},
		{"regex root base", "/", "~/model/(?<model_name>[^/]+)/converse(?:-stream)?", bedrock},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, RoutePath(tc.base, tc.spec), "RoutePath(%q) [%s]", tc.base, tc.name)
	}
}
