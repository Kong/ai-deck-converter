package kong

import (
	"strings"
	"testing"

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
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
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
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}

	// Empty top-level sections must be omitted.
	if strings.Contains(got, "consumers:") {
		t.Errorf("expected empty consumers to be omitted\n---\n%s", got)
	}
}
