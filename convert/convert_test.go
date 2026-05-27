package convert

import (
	"strings"
	"testing"
)

func TestConvertWarnsUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
`)
	_, warnings, err := Convert(src, Options{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsSubstr(warnings, "unknown provider") {
		t.Errorf("expected unknown-provider warning, got %v", warnings)
	}
}

func TestConvertStrictFailsUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
`)
	if _, _, err := Convert(src, Options{Strict: true}); err == nil {
		t.Error("expected strict mode to fail on unknown provider")
	}
}

func TestConvertWarnsUnknownPolicy(t *testing.T) {
	src := []byte(`
consumers:
  - name: c1
    type: api-key
    policies: [missing-policy]
`)
	_, warnings, err := Convert(src, Options{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsSubstr(warnings, "unknown policy") {
		t.Errorf("expected unknown-policy warning, got %v", warnings)
	}
}

func containsSubstr(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
