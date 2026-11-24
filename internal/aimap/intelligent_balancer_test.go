package aimap

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCostQualityTradeoff(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in     any
		want   float64
		wantOK bool
	}{
		"cost":           {in: "cost", want: 0, wantOK: true},
		"balanced":       {in: "balanced", want: 0.5, wantOK: true},
		"quality":        {in: "quality", want: 1, wantOK: true},
		"number":         {in: 0.25, want: 0.25, wantOK: true},
		"integer":        {in: 1, want: 1, wantOK: true},
		"unknown string": {in: "fast"},
		"below zero":     {in: -0.1},
		"above one":      {in: 2},
		"wrong type":     {in: true},
		"not a number":   {in: math.NaN()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := CostQualityTradeoffToPlugin(tc.in)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.InDelta(t, tc.want, got, 0)
			}
		})
	}

	require.Equal(t, "cost", CostQualityTradeoffFromPlugin(0))
	require.Equal(t, "balanced", CostQualityTradeoffFromPlugin(0.5))
	require.Equal(t, "quality", CostQualityTradeoffFromPlugin(1.0))
	require.InDelta(t, 0.25, CostQualityTradeoffFromPlugin(0.25), 0)
}

func TestClassifierAuthRoundTrip(t *testing.T) {
	t.Parallel()

	api := map[string]any{
		"type": "custom",
		"url":  "https://classifier.internal",
		"auth": map[string]any{
			"type":    "basic",
			"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer k"}},
		},
	}
	plugin := ClassifierToPlugin(api)
	require.Equal(t, map[string]any{"header_name": "Authorization", "header_value": "Bearer k"}, plugin["auth"])
	require.Equal(t, "https://classifier.internal", plugin["url"])
	require.Equal(t, api, ClassifierFromPlugin(plugin))

	noHeaders := ClassifierToPlugin(map[string]any{"type": "custom", "auth": map[string]any{"type": "basic"}})
	require.NotContains(t, noHeaders, "auth")

	noAuth := map[string]any{"type": "custom"}
	require.Equal(t, noAuth, ClassifierToPlugin(noAuth))
	require.Equal(t, noAuth, ClassifierFromPlugin(noAuth))
	require.Equal(t, noAuth, ClassifierFromPlugin(map[string]any{"type": "custom", "auth": map[string]any{}}))
}

func TestClassifierAuthDropped(t *testing.T) {
	t.Parallel()

	require.Nil(t, ClassifierAuthDropped(map[string]any{"type": "custom"}))
	require.Empty(t, ClassifierAuthDropped(map[string]any{"auth": map[string]any{
		"type":    "basic",
		"headers": []any{map[string]any{"name": "A", "value": "a"}},
	}}))
	require.Equal(t, []string{"params", "headers[1]"}, ClassifierAuthDropped(map[string]any{"auth": map[string]any{
		"type":    "basic",
		"params":  []any{map[string]any{"name": "key", "value": "k"}},
		"headers": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}},
	}}))
}
