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

	base, ok := ClassifierBaseURL("http://kev.internal:8009/v1/systemone")
	require.True(t, ok)
	require.Equal(t, "http://kev.internal:8009", base)
	base, ok = ClassifierBaseURL("https://gw.example.com/decide/v1/systemone")
	require.True(t, ok)
	require.Equal(t, "https://gw.example.com/decide", base)
	_, ok = ClassifierBaseURL("https://openrouter.ai/api/alpha/decisions")
	require.False(t, ok)

	require.Equal(t, "http://kev:8009/v1/systemone", JoinClassifierURL("http://kev:8009", ClassifierFormat))
	require.Equal(t, "http://kev:8009/v1/systemone", JoinClassifierURL("http://kev:8009/", ClassifierFormat))

	require.Empty(t, ClassifierBaseURLProblem("https://kev.internal"))
	require.Empty(t, ClassifierBaseURLProblem("http://kev.internal:8009/prefix"))
	require.NotEmpty(t, ClassifierBaseURLProblem("ftp://kev.internal"))
	require.NotEmpty(t, ClassifierBaseURLProblem("https://"))
	require.NotEmpty(t, ClassifierBaseURLProblem("https://kev.internal/x?a=b"))
	require.NotEmpty(t, ClassifierBaseURLProblem("https://kev.internal/x#f"))
	require.NotEmpty(t, ClassifierBaseURLProblem("https://user@kev.internal"))
}
