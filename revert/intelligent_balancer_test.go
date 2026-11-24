package revert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntelligentBalancerFromConfig(t *testing.T) {
	t.Parallel()

	got := balancerFromConfig(map[string]any{
		"algorithm":             "intelligent",
		"cost_quality_tradeoff": 0.0,
		"classifier": map[string]any{
			"type": "custom",
			"auth": map[string]any{"header_name": "Authorization", "header_value": "Bearer k"},
		},
	}, nil, nil, nil)

	require.Equal(t, "intelligent", got.Algorithm)
	require.Equal(t, "cost", got.Fields["cost_quality_tradeoff"])
	require.Equal(t, map[string]any{
		"type":    "basic",
		"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer k"}},
	}, got.Fields["classifier"].(map[string]any)["auth"])
}

// Another algorithm keeps its fields as they are.
func TestBalancerFromConfigKeepsOtherAlgorithms(t *testing.T) {
	t.Parallel()

	got := balancerFromConfig(map[string]any{"algorithm": "lowest-usage", "cost_quality_tradeoff": 0.0},
		nil, nil, nil)
	require.InDelta(t, 0.0, got.Fields["cost_quality_tradeoff"], 0)
}
