package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollapseSanitizerAnonymize(t *testing.T) {
	cases := map[string]struct {
		in   any
		want any
	}{
		"both":             {[]any{"all", "credentials"}, []any{"all_and_credentials"}},
		"order kept":       {[]any{"email", "credentials", "phone", "all"}, []any{"email", "all_and_credentials", "phone"}},
		"already combined": {[]any{"all", "all_and_credentials", "credentials"}, []any{"all_and_credentials"}},
		"string slice":     {[]string{"all", "credentials"}, []any{"all_and_credentials"}},
		"all only":         {[]any{"all", "email"}, []any{"all", "email"}},
		"credentials only": {[]any{"credentials"}, []any{"credentials"}},
		"combined only":    {[]any{"all_and_credentials"}, []any{"all_and_credentials"}},
		"no anonymize":     {nil, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := map[string]any{"stop_on_error": true}
			if tc.in != nil {
				cfg["anonymize"] = tc.in
			}
			got := CollapseSanitizerAnonymize(cfg)
			require.Equal(t, tc.want, got["anonymize"])
			require.Equal(t, true, got["stop_on_error"])
			require.Equal(t, tc.in, cfg["anonymize"], "source config changed")
		})
	}
}
