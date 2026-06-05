package aimap

import "testing"

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
		if got := SectionFor(tc.format, tc.providerType); got != tc.want {
			t.Errorf("SectionFor(%q,%q) = %q, want %q", tc.format, tc.providerType, got, tc.want)
		}
	}
}

func TestEndpointLookupAndNormalization(t *testing.T) {
	// chat -> generate -> openai chat completions.
	for _, capability := range NormalizeCapability("chat") {
		spec, ok := LookupEndpoint("openai", capability)
		if !ok || spec.RouteType != "llm/v1/chat" || spec.PathSuffix != "/chat/completions" {
			t.Errorf("openai chat lookup = %+v ok=%v", spec, ok)
		}
	}
	// bare audio fans out to three endpoints.
	if got := NormalizeCapability("audio"); len(got) != 3 {
		t.Errorf("NormalizeCapability(audio) = %v, want 3 entries", got)
	}
	// batch alias.
	if got := NormalizeCapability("batch"); len(got) != 1 || got[0] != "batches" {
		t.Errorf("NormalizeCapability(batch) = %v", got)
	}
	// unsupported (section,capability) returns not-ok.
	if _, ok := LookupEndpoint("anthropic", "image"); ok {
		t.Error("expected anthropic image to be unsupported")
	}
}
