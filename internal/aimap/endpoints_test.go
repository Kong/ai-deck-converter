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
	// gemini folds in the Vertex-only image/video/rerank capabilities, generate first.
	require.Equal(t,
		[]string{"generate", "batches", "embeddings", "files", "image", "rerank", "video"},
		CapabilitiesFor("gemini"))
	// A format with no rendering section is unaffected.
	require.Equal(t, []string{"rerank"}, CapabilitiesFor("cohere"))
	// A rendering section is not a format, and an unknown format yields nil.
	require.Nil(t, CapabilitiesFor("vertex"))
	require.Nil(t, CapabilitiesFor("nope"))
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
