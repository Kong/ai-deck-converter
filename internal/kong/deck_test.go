package kong

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestMarshalShape(t *testing.T) {
	port := 80
	doc := NewDocument()
	doc.Services = []Service{{
		Name:     "svc",
		Host:     "localhost",
		Port:     &port,
		Protocol: "http",
		Routes: []Route{{
			Name:  "rt",
			Paths: []string{"/chat"},
			Plugins: []Plugin{{
				Name:   "ai-proxy-advanced",
				Config: map[string]any{"llm_format": "openai"},
			}},
		}},
	}}

	out, err := yaml.Marshal(doc)
	require.NoError(t, err, "marshal")
	got := string(out)

	for _, want := range []string{
		"_format_version: \"3.0\"",
		"services:",
		"name: svc",
		"routes:",
		"name: rt",
		"plugins:",
		"name: ai-proxy-advanced",
		"llm_format: openai",
	} {
		require.Contains(t, got, want, "output missing %q", want)
	}

	// Empty top-level sections must be omitted.
	require.NotContains(t, got, "consumers:", "expected empty consumers to be omitted")
}
