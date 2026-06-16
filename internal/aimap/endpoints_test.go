package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
