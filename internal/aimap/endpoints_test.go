package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormats(t *testing.T) {
	got := Formats()
	// The valid Format.Type values, i.e. EndpointTable sections minus provider renderings.
	want := []string{"anthropic", "bedrock", "cohere", "gemini", "huggingface", "openai", "typesafe"}
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
		{"gemini", "vertex", "batches", "gemini"},
		// ...but keeps the Vertex section for its exclusive capabilities.
		{"gemini", "vertex", "image", "vertex"},
		{"gemini", "vertex", "video", "vertex"},
		{"gemini", "vertex", "rerank", "vertex"},
		// Vertex does not implement Gemini's Files API; don't map it to gemini.
		{"gemini", "vertex", "files", "vertex"},
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
	video, videoOK := LookupEndpoint("openai", "video")
	require.True(t, videoOK, "openai video lookup ok")
	require.Equal(t, "/videos", video.PathSuffix, "openai video path suffix")
	require.Equal(t, []string{"POST"}, video.Methods, "openai video create method")
	// unsupported (section,capability) returns not-ok.
	_, ok := LookupEndpoint("anthropic", "image")
	require.False(t, ok, "expected anthropic image to be unsupported")
}

func TestEndpointsForAndSectionEndpoints(t *testing.T) {
	// bedrock generate is reachable via both its primary (converse) spec and
	// the secondary (invoke) spec it shares with audio/speech.
	specs, ok := EndpointsFor("bedrock", "generate")
	require.True(t, ok, "bedrock generate EndpointsFor ok")
	require.Len(t, specs, 2, "bedrock generate has a primary and one secondary spec")
	require.Equal(t, "converse", specs[0].RouteLabel, "primary spec is converse")
	require.Equal(t, "invoke", specs[1].RouteLabel, "secondary spec is invoke")
	require.Equal(t, EndpointTable["bedrock"]["audio/speech"].Primary, specs[1],
		"generate's secondary spec is reused verbatim from audio/speech")

	// A capability with no secondary endpoints returns just the primary spec.
	specs, ok = EndpointsFor("openai", "generate")
	require.True(t, ok, "openai generate EndpointsFor ok")
	require.Len(t, specs, 1, "openai generate has no secondary specs")

	// An unsupported (section, capability) pair is still not-ok.
	_, ok = EndpointsFor("anthropic", "image")
	require.False(t, ok, "anthropic image EndpointsFor not ok")

	// SectionEndpoints surfaces the same secondary spec alongside every
	// primary spec in the section, so scanning callers (revert's endpoint
	// resolution) see it as a candidate too.
	all := SectionEndpoints("bedrock")
	require.Len(t, all, len(EndpointTable["bedrock"])+1, "bedrock section has one secondary entry")
	var sawSecondaryGenerate bool
	for _, ce := range all {
		if ce.Capability == "generate" && ce.Spec.RouteLabel == "invoke" {
			sawSecondaryGenerate = true
		}
	}
	require.True(t, sawSecondaryGenerate, "SectionEndpoints includes generate's secondary invoke spec")

	// A section with no secondaries returns exactly the primary specs.
	require.Len(t, SectionEndpoints("openai"), len(EndpointTable["openai"]),
		"openai section has no secondary endpoints")
}

func TestSkillsCapability(t *testing.T) {
	// The skills API is a restful CRUD surface on its own route, per provider section.
	for section, wantPath := range map[string]string{"openai": "/skills", "anthropic": "/v1/skills"} {
		spec, ok := LookupEndpoint(section, "skills")
		require.True(t, ok, "%s skills lookup ok", section)
		require.Equal(t, "llm/v1/skills", spec.RouteType, "%s skills route type", section)
		require.Equal(t, "skills", spec.RouteLabel, "%s skills route label", section)
		require.Equal(t, wantPath, spec.PathSuffix, "%s skills path suffix", section)
		require.Equal(t, mGetPostDelete, spec.Methods, "%s skills methods", section)
		require.Equal(t, catTextGen, spec.GenaiCategory, "%s skills category", section)
		// Kong does not support log statistics for skills, and the routes carry no model.
		require.False(t, spec.SupportsLogStatistics, "%s skills log statistics", section)
		require.Nil(t, spec.DefaultModelSelectorConfig, "%s skills model selector", section)
	}
	// No other format serves it.
	_, ok := LookupEndpoint("gemini", "skills")
	require.False(t, ok, "gemini does not serve skills")

	// Passthrough-only: offered only when the provider renders the model's own format.
	require.True(t, RequiresNativeFormat("skills"), "skills requires a native format")
	require.False(t, RequiresNativeFormat("generate"), "generate is converted across formats")
	require.Contains(t, CapabilitiesFor("openai", "openai"), "skills")
	require.Contains(t, CapabilitiesFor("anthropic", "anthropic"), "skills")
	require.NotContains(t, CapabilitiesFor("openai", "anthropic"), "skills")
	require.NotContains(t, CapabilitiesFor("anthropic", "openai"), "skills")
}

func TestRoutePath(t *testing.T) {
	chat, _ := LookupEndpoint("openai", "generate")
	bedrock, _ := LookupEndpoint("bedrock", "generate")
	geminiBatches, _ := LookupEndpoint("gemini", "batches")
	cases := []struct {
		name, base, want string
		spec             EndpointSpec
	}{
		{"plain base", "/ai", "/ai/chat/completions", chat},
		{"root base does not double slash", "/", "/chat/completions", chat},
		{"empty base", "", "/chat/completions", chat},
		{"trailing slash trimmed", "/ai/", "/ai/chat/completions", chat},
		{"gemini batches joins with separator", "/gm", "/gm/v1beta/batches", geminiBatches},
		{"gemini batches root base", "/", "/v1beta/batches", geminiBatches},
		{"regex plain base", "/ai", "~/ai/model/(?<model_name>[^/]+)/converse(?:-stream)?", bedrock},
		{"regex root base", "/", "~/model/(?<model_name>[^/]+)/converse(?:-stream)?", bedrock},
		{"regex base retains one marker", "~/ai", "~/ai/model/(?<model_name>[^/]+)/converse(?:-stream)?", bedrock},
		{"regex root base retains one marker", "~/", "~/model/(?<model_name>[^/]+)/converse(?:-stream)?", bedrock},
		{
			"regex base with non-regex suffix keeps marker",
			"~^/deployments/(?<id>[^/]+)", "~^/deployments/(?<id>[^/]+)/chat/completions", chat,
		},
		{
			"regex base end-anchor stripped before non-regex suffix",
			"~^/deployments/(?<id>[^/]+)$", "~^/deployments/(?<id>[^/]+)/chat/completions", chat,
		},
		{
			"regex base end-anchor stripped before regex suffix",
			"~^/deployments/(?<id>[^/]+)$",
			"~^/deployments/(?<id>[^/]+)/model/(?<model_name>[^/]+)/converse(?:-stream)?",
			bedrock,
		},
		{
			"escaped literal dollar at end is preserved",
			`~^/pay/(?<amt>[0-9]+)\$`, `~^/pay/(?<amt>[0-9]+)\$/chat/completions`, chat,
		},
		{
			"escaped dollar kept while real end-anchor is stripped",
			`~^/pay/(?<amt>\$[0-9]+)$`, `~^/pay/(?<amt>\$[0-9]+)/chat/completions`, chat,
		},
		{
			"escaped backslash before real anchor strips only the anchor",
			`~^/x\\$`, `~^/x\\/chat/completions`, chat,
		},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, RoutePath(tc.base, tc.spec), "RoutePath(%q) [%s]", tc.base, tc.name)
	}
}
