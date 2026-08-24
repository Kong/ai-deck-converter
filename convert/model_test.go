package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
)

func TestNormalizeNamedCaptures(t *testing.T) {
	cases := map[string]string{
		"~/openai/(?<m>[^/]+)":     "~/openai/(?P<m>[^/]+)",
		"~/openai/(?P<m>[^/]+)":    "~/openai/(?P<m>[^/]+)",
		"~/openai/(?'m'[^/]+)":     "~/openai/(?P<m>[^/]+)",
		"~/a/(?<x>[^/]+)/(?'y'.+)": "~/a/(?P<x>[^/]+)/(?P<y>.+)",
		"/no/captures/here":        "/no/captures/here",
		"~/(?:non-capturing)/x":    "~/(?:non-capturing)/x",
	}
	for in, want := range cases {
		require.Equal(t, want, normalizeNamedCaptures(in), "normalizeNamedCaptures(%q)", in)
	}
}

func TestPathParamCapturedAllSyntaxes(t *testing.T) {
	for _, path := range []string{
		"~/openai/(?<model>[^/]+)",
		"~/openai/(?P<model>[^/]+)",
		"~/openai/(?'model'[^/]+)",
	} {
		require.Truef(t, pathParamCaptured([]string{path}, "model", false), "path %q should be detected", path)
	}

	// A capture whose name does not match, a non-regex path, and a path with no
	// capture at all are all rejected.
	require.False(t, pathParamCaptured([]string{"~/openai/(?<other>[^/]+)"}, "model", false))
	require.False(t, pathParamCaptured([]string{"/openai/(?<model>[^/]+)"}, "model", false))
	require.False(t, pathParamCaptured([]string{"~/openai/[^/]+"}, "model", false))

	// Every path in the set must carry the capture.
	require.False(t, pathParamCaptured(
		[]string{"~/openai/(?<model>[^/]+)", "~/alt/(?<other>[^/]+)"}, "model", false))
}

func TestTryConvertPCREToLuaAllSyntaxes(t *testing.T) {
	for _, in := range []string{
		"~/openai/(?<m>[^:/]+)",
		"~/openai/(?P<m>[^:/]+)",
		"~/openai/(?'m'[^:/]+)",
	} {
		require.Equalf(t, "/openai/([%w%.%-:]+)", tryConvertPCREToLua(in), "input %q", in)
	}
	// No named capture falls back to the format default.
	require.Equal(t, aimap.OpenAIDefaultPathPattern, tryConvertPCREToLua("~/openai/[^/]+"))
}
