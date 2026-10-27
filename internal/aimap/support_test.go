package aimap

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
)

func spec(t *testing.T, section, capability string) EndpointSpec {
	t.Helper()
	s, ok := LookupEndpoint(section, capability)
	require.True(t, ok, "%s %s", section, capability)
	return s
}

func TestTargetSupportsConditions(t *testing.T) {
	openai := Target{ProviderType: "openai"}
	azure := Target{ProviderType: "azure"}
	anthropic := Target{ProviderType: "anthropic"}
	bedrock := Target{ProviderType: "bedrock"}
	gemini := Target{ProviderType: "gemini"}
	cases := []struct {
		name   string
		format string
		target Target
		spec   EndpointSpec
		want   bool
	}{
		// 1. The format accepts the provider.
		{"openai format accepts any provider", "openai", bedrock, spec(t, "openai", "generate"), true},
		{"native format accepts its provider", "bedrock", bedrock, spec(t, "bedrock", "generate"), true},
		{"native format rejects another provider", "bedrock", openai, spec(t, "bedrock", "generate"), false},
		{"anthropic format accepts any provider on chat", "anthropic", openai, spec(t, "anthropic", "generate"), true},
		{"anthropic format rejects another provider off chat", "anthropic", openai, spec(t, "anthropic", "batches"), false},
		// 2. The route type accepts the provider.
		{"route type accepts the provider", "openai", azure, spec(t, "openai", "audio/speech"), true},
		{"route type rejects the provider", "openai", anthropic, spec(t, "openai", "embeddings"), false},
		// 3. A gemini target on realtime uses the gemini format.
		{"gemini realtime in gemini format", "gemini", gemini, spec(t, "gemini", "realtime"), true},
		{"gemini realtime in openai format", "openai", gemini, spec(t, "openai", "realtime"), false},
		// 4. A mantle target uses openai or anthropic on a mantle route type.
		{
			"mantle chat in openai format", "openai",
			Target{ProviderType: "bedrock", Mantle: true},
			spec(t, "openai", "generate"), true,
		},
		{
			"mantle embeddings in openai format", "openai",
			Target{ProviderType: "bedrock", Mantle: true},
			spec(t, "openai", "embeddings"), false,
		},
		{
			"mantle files replaces the bedrock row", "openai",
			Target{ProviderType: "bedrock", Mantle: true},
			spec(t, "openai", "files"), true,
		},
		{
			"mantle in bedrock format", "bedrock",
			Target{ProviderType: "bedrock", Mantle: true},
			spec(t, "bedrock", "generate"), false,
		},
		// 5. A bedrock target on batches sets batch_bucket_prefix.
		{
			"bedrock batches with prefix", "bedrock",
			Target{ProviderType: "bedrock", BatchBucketPrefix: true},
			spec(t, "bedrock", "batches"), true,
		},
		{"bedrock batches without prefix", "bedrock", bedrock, spec(t, "bedrock", "batches"), false},
		// 6. An openai or anthropic target on skills uses its own format.
		{"openai skills in openai format", "openai", openai, spec(t, "openai", "skills"), true},
		{"anthropic skills in openai format", "openai", anthropic, spec(t, "openai", "skills"), false},
		// 7. A llama2 target with format raw is not on embeddings.
		{
			"llama2 raw on embeddings", "openai",
			Target{ProviderType: "llama2", Llama2RawFormat: true},
			spec(t, "openai", "embeddings"), false,
		},
		{
			"llama2 raw on chat", "openai",
			Target{ProviderType: "llama2", Llama2RawFormat: true},
			spec(t, "openai", "generate"), true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, TargetSupports(tc.format, tc.target, tc.spec))
		})
	}
}

func TestNativeSectionsAcceptTheirProvider(t *testing.T) {
	for section := range EndpointTable {
		target := Target{ProviderType: section, BatchBucketPrefix: true}
		enterprise := target
		enterprise.GeminiEnterprise = true
		for _, ce := range SectionEndpoints(section) {
			require.True(t,
				TargetSupports(section, target, ce.Spec) || TargetSupports(section, enterprise, ce.Spec),
				"%s %s %s", section, ce.Capability, ce.Spec.RouteLabel)
		}
	}
}

func TestLogStatisticsSupported(t *testing.T) {
	for routeType, providers := range unsupportedLogStatistics {
		for _, provider := range providers {
			s := EndpointSpec{RouteType: routeType, SupportsLogStatistics: true}
			require.False(t, LogStatisticsSupported(provider, s), "%s %s", routeType, provider)
		}
	}
	require.False(t, LogStatisticsSupported("bedrock", spec(t, "openai", "image")))
	require.True(t, LogStatisticsSupported("openai", spec(t, "openai", "image")))
	require.False(t, LogStatisticsSupported("openai", spec(t, "openai", "audio/transcription")))
}

func TestSupportsModelNameHeader(t *testing.T) {
	for _, capability := range []string{"batches", "files", "skills"} {
		require.False(t, SupportsModelNameHeader(spec(t, "openai", capability)), capability)
	}
	require.True(t, SupportsModelNameHeader(spec(t, "openai", "generate")))
}

func TestPairOrder(t *testing.T) {
	require.Equal(t, []string{"openai", "anthropic", "gemini"},
		OrderFormats([]string{"gemini", "openai", "anthropic", "gemini"}))
	require.Equal(t, []string{"generate", "embeddings", "image"},
		OrderCapabilities([]string{"image", "embeddings", "generate"}))
}

func TestSupportedFormats(t *testing.T) {
	targets := []Target{{ProviderType: "openai"}, {ProviderType: "azure"}}
	require.Equal(t, []aigw.Format{{Type: "openai"}, {Type: "anthropic"}}, SupportedFormats(targets, LatestVersion))

	mantle := []Target{{ProviderType: "bedrock", Mantle: true}}
	require.Equal(t, []aigw.Format{{Type: "openai"}, {Type: "anthropic"}}, SupportedFormats(mantle, LatestVersion))

	typesafe := []Target{{ProviderType: "typesafe"}}
	require.Equal(t, []aigw.Format{{Type: "openai"}, {Type: "anthropic"}, {Type: "typesafe"}},
		SupportedFormats(typesafe, LatestVersion))
	require.Equal(t, []aigw.Format{{Type: "openai"}, {Type: "anthropic"}},
		SupportedFormats(typesafe, mustVersion("2.1")))
}

func TestSupportedCapabilities(t *testing.T) {
	openai := Target{ProviderType: "openai"}
	require.Equal(t,
		[]aigw.Capability{
			"generate", "agentic", "audio/speech", "audio/transcription", "audio/translation",
			"embeddings", "image", "realtime", "skills", "video",
		},
		SupportedCapabilities(aigw.Format{Type: "openai"}, openai, "model", LatestVersion))
	require.Equal(t, []aigw.Capability{"batches", "files"},
		SupportedCapabilities(aigw.Format{Type: "openai"}, openai, "api", LatestVersion))

	// The skills gate leaves out skills below 2.2.
	require.NotContains(t,
		SupportedCapabilities(aigw.Format{Type: "openai"}, openai, "model", mustVersion("2.1")), "skills")
	// The gemini agentic gate applies only in the gemini section.
	require.NotContains(t,
		SupportedCapabilities(aigw.Format{Type: "gemini"}, Target{ProviderType: "gemini"}, "model", mustVersion("2.2")),
		"agentic")

	// generate claims the invoke route first, so the other genai categories
	// lose it, and invoke is their only endpoint.
	require.Equal(t, []aigw.Capability{"generate", "agentic", "audio/speech", "rerank"},
		SupportedCapabilities(aigw.Format{Type: "bedrock"}, Target{ProviderType: "bedrock"}, "model", LatestVersion))
}

func TestGeminiInteractionsNeedProjectAndLocation(t *testing.T) {
	for _, ce := range SectionEndpoints("gemini") {
		if ce.Capability != "agentic" {
			continue
		}
		enterprise := isGeminiEnterpriseInteractions(ce.Spec)
		require.Equal(t, !enterprise, TargetSupports("gemini", Target{ProviderType: "gemini"}, ce.Spec), ce.Spec.RouteLabel)
		require.Equal(t, enterprise,
			TargetSupports("gemini", Target{ProviderType: "gemini", GeminiEnterprise: true}, ce.Spec), ce.Spec.RouteLabel)
	}
}
