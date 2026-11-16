package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelBodySize(t *testing.T) {
	saved := ProviderMaxBodySize
	t.Cleanup(func() { ProviderMaxBodySize = saved })
	ProviderMaxBodySize = map[string]int{"anthropic": 32_000_000, "small": 1_000_000}

	for name, tc := range map[string]struct {
		types []string
		want  int
		ok    bool
	}{
		"no providers":     {types: nil},
		"published":        {types: []string{"anthropic"}, want: 32_000_000, ok: true},
		"smallest wins":    {types: []string{"anthropic", "small"}, want: 1_000_000, ok: true},
		"one unpublished":  {types: []string{"anthropic", "openai"}},
		"only unpublished": {types: []string{"openai"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := DefaultModelBodySize(tc.types)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}
