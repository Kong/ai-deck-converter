package revert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

func TestDetectProviderType(t *testing.T) {
	cases := []struct {
		enum, path, want string
	}{
		{"openai", "/ai/chat/completions", "openai"},
		{"bedrock", "~/ai/model/(?<model_name>[^/]+)/converse(?:-stream)?", "bedrock"},
		{"gemini", "~/ai/v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)", "gemini"},
		{"gemini", "~/ai/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)", "vertex"},
		{"gemini", "", "gemini"},
	}
	for _, tc := range cases {
		if got := detectProviderType(tc.enum, tc.path); got != tc.want {
			t.Errorf("detectProviderType(%q, %q) = %q, want %q", tc.enum, tc.path, got, tc.want)
		}
	}
}

func TestBasePathRecovery(t *testing.T) {
	cases := []struct {
		section, capability, path, wantBase string
		wantOK                              bool
	}{
		{"openai", "generate", "/ai/chat/completions", "/ai", true},
		{"openai", "generate", "/custom/base/chat/completions", "/custom/base", true},
		{"bedrock", "generate", "~/ai/model/(?<model_name>[^/]+)/converse(?:-stream)?", "/ai", true},
		{"vertex", "generate", "~/llm/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)", "/llm", true},
		{"openai", "generate", "/ai/embeddings", "", false},
		{"bedrock", "generate", "/ai/chat/completions", "", false}, // regex spec, literal path
	}
	for _, tc := range cases {
		spec, ok := aimap.LookupEndpoint(tc.section, tc.capability)
		if !ok {
			t.Fatalf("missing endpoint spec for %s/%s", tc.section, tc.capability)
		}
		base, ok := basePathFor(tc.path, spec)
		if ok != tc.wantOK || base != tc.wantBase {
			t.Errorf("basePathFor(%q, %s/%s) = (%q, %v), want (%q, %v)", tc.path, tc.section, tc.capability, base, ok, tc.wantBase, tc.wantOK)
		}
	}
}

func TestResolveEndpointDisambiguation(t *testing.T) {
	// bedrock invoke serves four capabilities with the same route label and
	// path; the target's route_type + genai_category pick the right one.
	m, ok := resolveEndpoint("bedrock", "image/v1/images/generations", "image/generation", "bedrock-invoke", "~/ai/model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?")
	if !ok || m.capability != "image" {
		t.Errorf("bedrock invoke image = (%+v, %v), want capability image", m, ok)
	}
	// anthropic generate vs batches share route_type llm/v1/chat; the route
	// name (or path) disambiguates.
	m, ok = resolveEndpoint("anthropic", "llm/v1/chat", "text/generation", "anthropic-batches", "/ai/v1/messages/batches")
	if !ok || m.capability != "batches" {
		t.Errorf("anthropic batches = (%+v, %v), want capability batches", m, ok)
	}
	// A route_type the section's table doesn't use still resolves via the
	// route name / path shape.
	m, ok = resolveEndpoint("anthropic", "llm/v1/batches", "", "anthropic-batches", "/ai/v1/messages/batches")
	if !ok || m.capability != "batches" {
		t.Errorf("anthropic generic batches = (%+v, %v), want capability batches", m, ok)
	}
}

func TestDeriveModelName(t *testing.T) {
	cases := map[string]string{
		"@openai/gpt-5.2":  "gpt-5-2",
		"@vertex/gem.1":    "gem-1",
		"plain-alias":      "plain-alias",
		"with/slash.alias": "with-slash-alias",
	}
	for alias, want := range cases {
		if got := deriveModelName(alias); got != want {
			t.Errorf("deriveModelName(%q) = %q, want %q", alias, got, want)
		}
	}
}

func TestProviderDedupAndNaming(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: gw
    url: http://gw.invalid
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        plugins:
          - name: ai-proxy-advanced
            config:
              llm_format: openai
              targets:
                - route_type: llm/v1/chat
                  auth: {header_name: Authorization, header_value: '{vault://env/key}'}
                  model: {provider: openai, name: gpt-4o, model_alias: '@a/one'}
                - route_type: llm/v1/chat
                  auth: {header_name: Authorization, header_value: '{vault://env/key}'}
                  model: {provider: openai, name: gpt-4o-mini, model_alias: '@a/two'}
                - route_type: llm/v1/chat
                  auth: {header_name: Authorization, header_value: '{vault://other/key}'}
                  model: {provider: openai, name: gpt-3.5, model_alias: '@a/three'}
`)
	doc, warnings, err := revertYAML(t, in, Options{})
	if err != nil {
		t.Fatalf("revert: %v (warnings: %v)", err, warnings)
	}
	if len(doc.Providers) != 2 {
		t.Fatalf("got %d providers, want 2 (identical auth must dedupe): %+v", len(doc.Providers), doc.Providers)
	}
	if doc.Providers[0].Name != "openai-env" || doc.Providers[1].Name != "openai-other" {
		t.Errorf("provider names = %q, %q; want openai-env, openai-other", doc.Providers[0].Name, doc.Providers[1].Name)
	}
}

func TestLegacyConfigWithoutAIModels(t *testing.T) {
	// Older gateways predate the ai-models entity and the ai-model-selector
	// plugin. When the document declares no ai-models entries at all, the
	// naming fallbacks (derive from alias, route name) run without warning —
	// and strict mode must succeed.
	in := []byte(`
_format_version: "3.0"
services:
  - name: gw
    url: http://gw.invalid
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        plugins:
          - name: ai-proxy-advanced
            config:
              llm_format: openai
              targets:
                - route_type: llm/v1/chat
                  model: {provider: openai, name: gpt-4o, model_alias: '@openai/gpt-4o'}
      - name: openai-embeddings
        paths: [/ai/embeddings]
        plugins:
          - name: ai-proxy-advanced
            config:
              llm_format: openai
              targets:
                - route_type: llm/v1/embeddings
                  model: {provider: openai, name: text-embedding-3-large}
`)
	doc, warnings, err := revertYAML(t, in, Options{Strict: true})
	if err != nil {
		t.Fatalf("strict revert: %v (warnings: %v)", err, warnings)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v; want none for a legacy config with no ai-models", warnings)
	}
	if len(doc.Models) != 2 || doc.Models[0].Name != "gpt-4o" || doc.Models[1].Name != "openai-embeddings" {
		t.Errorf("models = %+v; want gpt-4o (derived from alias) and openai-embeddings (route name)", doc.Models)
	}
}

func TestRevertModelACLsFromAIProxyAdvanced(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: gw
    url: http://gw.invalid
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        plugins:
          - name: ai-proxy-advanced
            config:
              acls:
                allow: [consumer-alice, group-admins]
                deny: [consumer-bob, group-blocked]
              llm_format: openai
              targets:
                - route_type: llm/v1/chat
                  model: {provider: openai, name: gpt-4o, model_alias: '@openai/m1'}
ai-models:
  - name: m1
    alias: '@openai/m1'
`)

	doc, warnings, err := revertYAML(t, in, Options{})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(doc.Models) != 1 {
		t.Fatalf("expected 1 model, got %d: %+v", len(doc.Models), doc.Models)
	}
	if got := doc.Models[0].ACLs.Allow; len(got) != 2 || got[0] != "consumer-alice" || got[1] != "group-admins" {
		t.Fatalf("unexpected allow ACLs: %#v", got)
	}
	if got := doc.Models[0].ACLs.Deny; len(got) != 2 || got[0] != "consumer-bob" || got[1] != "group-blocked" {
		t.Fatalf("unexpected deny ACLs: %#v", got)
	}
}

func TestMismatchedAliasStillWarns(t *testing.T) {
	// When ai-models entries exist but a target alias matches none of them,
	// that is a genuine inconsistency and keeps its warning.
	in := []byte(`
_format_version: "3.0"
services:
  - name: gw
    url: http://gw.invalid
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        plugins:
          - name: ai-proxy-advanced
            config:
              llm_format: openai
              targets:
                - route_type: llm/v1/chat
                  model: {provider: openai, name: gpt-4o, model_alias: '@openai/gpt-4o'}
ai-models:
  - name: other-model
    alias: '@openai/other'
`)
	_, warnings, err := revertYAML(t, in, Options{})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "no ai-models entry for alias") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; want a no-ai-models-entry-for-alias warning", warnings)
	}
}

func TestStrictModeMakesDropsFatal(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: orphan
    url: http://nowhere.invalid
`)
	if _, warnings, err := revertYAML(t, in, Options{}); err != nil || len(warnings) != 1 {
		t.Errorf("non-strict: err=%v warnings=%v; want nil error and 1 warning", err, warnings)
	}
	if _, _, err := revertYAML(t, in, Options{Strict: true}); err == nil || !strings.Contains(err.Error(), "no routes") {
		t.Errorf("strict: err=%v; want a no-routes error", err)
	}
}

func TestUnresolvableCapabilityDefaultsToGenerate(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: gw
    url: http://gw.invalid
    routes:
      - name: weird
        paths: [/something/else]
        plugins:
          - name: ai-proxy-advanced
            config:
              llm_format: mistral
              targets:
                - route_type: llm/v1/chat
                  model: {provider: mistral, name: mistral-large, model_alias: '@m/large'}
`)
	doc, warnings, err := revertYAML(t, in, Options{})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if len(doc.Models) != 1 || len(doc.Models[0].Capabilities) != 1 || doc.Models[0].Capabilities[0] != "generate" {
		t.Fatalf("models = %+v; want one model with capability generate", doc.Models)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "defaulting to generate") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; want a defaulting-to-generate warning", warnings)
	}
}

// revertYAML is a test helper that runs Revert and re-parses the output into
// an aigw document for structural assertions.
func revertYAML(t *testing.T, in []byte, opts Options) (*aigw.Document, []string, error) {
	t.Helper()
	out, warnings, err := Revert(in, opts)
	if err != nil {
		return nil, warnings, err
	}
	doc, err := aigw.Parse(out)
	if err != nil {
		t.Fatalf("re-parse output: %v\n%s", err, out)
	}
	return doc, warnings, nil
}
