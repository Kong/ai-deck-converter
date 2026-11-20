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

func TestClassifierURLProblem(t *testing.T) {
	for _, url := range []string{
		"https://api.typesafe.ai", "http://kev.internal:8009", "https://kev.internal/",
		"https://openrouter.ai/api",
	} {
		require.Empty(t, ClassifierURLProblem(url), url)
	}
	for _, url := range []string{
		"ftp://kev.internal", "kev.internal", "https://", "https:///v1/systemone",
		"https://kev.internal/x?a=b", "https://kev.internal/x#f", "https://user@kev.internal",
	} {
		require.NotEmpty(t, ClassifierURLProblem(url), url)
	}
}
