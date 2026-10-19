package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPRateLimitingFromPluginWithoutLimits(t *testing.T) {
	for _, tt := range []struct {
		name  string
		block map[string]any
	}{
		{name: "nil block", block: nil},
		{name: "null limits", block: map[string]any{"limits": nil}},
		{name: "empty limits", block: map[string]any{"limits": []any{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Nil(t, MCPRateLimitingFromPlugin(tt.block))
		})
	}
}
