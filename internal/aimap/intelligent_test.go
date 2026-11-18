package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntelligentPresets(t *testing.T) {
	require.Equal(t, []string{"cost", "balanced", "quality"}, IntelligentPresetNames())
	for _, name := range IntelligentPresetNames() {
		p, ok := IntelligentPresetNamed(name)
		require.True(t, ok, name)
		got, exact := NearestTradeoffPreset(p.Tradeoff)
		require.True(t, exact, name)
		require.Equal(t, p, got, name)
		got, exact = NearestMarginPreset(p.Margin)
		require.True(t, exact, name)
		require.Equal(t, p, got, name)
	}
	_, ok := IntelligentPresetNamed("fast")
	require.False(t, ok)
	_, ok = IntelligentPresetNamed(DefaultIntelligentPreset)
	require.True(t, ok)
}

func TestNearestIntelligentPreset(t *testing.T) {
	tradeoffs := map[float64]string{
		0: "cost", 0.3: "cost", 0.4: "balanced", 0.6: "balanced", 0.7: "quality", 1: "quality",
		// A tie goes to the default preset.
		0.375: "balanced", 0.625: "balanced",
	}
	for v, want := range tradeoffs {
		got, exact := NearestTradeoffPreset(v)
		require.False(t, exact, v)
		require.Equal(t, want, got.Name, v)
	}
	margins := map[float64]string{
		100: "cost", 12: "cost", 8: "balanced", 3: "balanced", 1: "quality", 10: "balanced", 2.5: "balanced",
	}
	for v, want := range margins {
		got, exact := NearestMarginPreset(v)
		require.False(t, exact, v)
		require.Equal(t, want, got.Name, v)
	}
}

func TestClassifierURL(t *testing.T) {
	require.Equal(t, "/v1/systemone", ClassifierFormat)

	split := map[string][2]string{
		"http://kev.internal:8009/v1/systemone":       {"http://kev.internal:8009", "/v1/systemone"},
		"https://openrouter.ai/api/alpha/decisions":   {"https://openrouter.ai", "/api/alpha/decisions"},
		"https://gw.example.com/decide/v1/systemone/": {"https://gw.example.com", "/decide/v1/systemone/"},
		"http://kev.internal:8009":                    {"http://kev.internal:8009", "/v1/systemone"},
		"https://kev.internal/":                       {"https://kev.internal", "/v1/systemone"},
	}
	for in, want := range split {
		base, format, problem := SplitClassifierURL(in)
		require.Empty(t, problem, in)
		require.Equal(t, want, [2]string{base, format}, in)
	}
	for _, in := range []string{
		"ftp://kev.internal", "https://", "https:///v1/systemone", "https://kev.internal/x?a=b",
		"https://kev.internal/x#f", "https://user@kev.internal",
	} {
		_, _, problem := SplitClassifierURL(in)
		require.NotEmpty(t, problem, in)
	}

	require.Equal(t, "http://kev:8009/v1/systemone", JoinClassifierURL("http://kev:8009", ClassifierFormat))
	require.Equal(t, "http://kev:8009/v1/systemone", JoinClassifierURL("http://kev:8009/", ClassifierFormat))
}

func TestHasHostedClassifier(t *testing.T) {
	require.True(t, HasHostedClassifier(TypesafeProviderType))
	require.False(t, HasHostedClassifier("openai"))
}
