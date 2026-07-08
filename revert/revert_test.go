package revert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
)

func TestDetectProviderType(t *testing.T) {
	cases := []struct {
		enum, path, want string
	}{
		{"openai", "/ai/chat/completions", "openai"},
		{"bedrock", "~/ai/model/(?<model_name>[^/]+)/converse(?:-stream)?", "bedrock"},
		{
			"gemini", "~/ai/v1beta/models/(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			"gemini",
		},
		{
			"gemini",
			"~/ai/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			"vertex",
		},
		{"gemini", "", "gemini"},
	}
	for _, tc := range cases {
		got := detectProviderType(tc.enum, tc.path)
		require.Equalf(t, tc.want, got, "detectProviderType(%q, %q)", tc.enum, tc.path)
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
		{
			"vertex", "generate",
			"~/llm/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/" +
				"(?<model_name>[^:/]+):(?:generateContent|streamGenerateContent)",
			"/llm", true,
		},
		{"openai", "generate", "/ai/embeddings", "", false},
		{"bedrock", "generate", "/ai/chat/completions", "", false}, // regex spec, literal path
	}
	for _, tc := range cases {
		spec, ok := aimap.LookupEndpoint(tc.section, tc.capability)
		require.Truef(t, ok, "missing endpoint spec for %s/%s", tc.section, tc.capability)
		base, ok := basePathFor(tc.path, spec)
		require.Equalf(t, tc.wantOK, ok, "basePathFor(%q, %s/%s) ok", tc.path, tc.section, tc.capability)
		require.Equalf(t, tc.wantBase, base, "basePathFor(%q, %s/%s) base", tc.path, tc.section, tc.capability)
	}
}

func TestResolveEndpointDisambiguation(t *testing.T) {
	// bedrock invoke serves four capabilities with the same route label and
	// path; the target's route_type + genai_category pick the right one.
	m, ok := resolveEndpoint("bedrock", "image/v1/images/generations", "image/generation",
		"bedrock-invoke", "~/ai/model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?")
	require.True(t, ok, "bedrock invoke image ok")
	require.Equal(t, "image", m.capability, "bedrock invoke image capability")
	// bedrock generate/agentic/rerank/audio-speech all share route_type
	// llm/v1/chat; the route name (RouteLabel) disambiguates.
	m, ok = resolveEndpoint("bedrock", "llm/v1/chat", "text/generation",
		"bedrock-invoke", "~/ai/model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?")
	require.True(t, ok, "bedrock invoke audio ok")
	require.Equal(t, "audio/speech", m.capability, "bedrock invoke audio-speech capability")
	// A route_type the section's table doesn't use still resolves via the
	// route name / path shape.
	m, ok = resolveEndpoint("anthropic", "llm/v1/completions", "", "anthropic-messages", "/ai/v1/messages")
	require.True(t, ok, "anthropic generic generate ok")
	require.Equal(t, "generate", m.capability, "anthropic generic generate capability")
}

func TestDeriveModelName(t *testing.T) {
	cases := map[string]string{
		"@openai/gpt-5.2":  "gpt-5-2",
		"@vertex/gem.1":    "gem-1",
		"plain-alias":      "plain-alias",
		"with/slash.alias": "with-slash-alias",
	}
	for alias, want := range cases {
		got := deriveModelName(alias)
		require.Equalf(t, want, got, "deriveModelName(%q)", alias)
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
	require.NoErrorf(t, err, "revert (warnings: %v)", warnings)
	require.Lenf(t, doc.Providers, 2, "identical auth must dedupe: %+v", doc.Providers)
	require.Equal(t, "openai-env", doc.Providers[0].Name, "first provider name")
	require.Equal(t, "openai-other", doc.Providers[1].Name, "second provider name")
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
	require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
	require.Empty(t, warnings, "want no warnings for a legacy config with no ai-models")
	require.Lenf(t, doc.Models, 2, "models = %+v", doc.Models)
	require.Equal(t, "gpt-4o", doc.Models[0].Name, "first model name (derived from alias)")
	require.Equal(t, "openai-embeddings", doc.Models[1].Name, "second model name (route name)")
}

func TestAliaslessTargetsCanUseAIModelsNameOnly(t *testing.T) {
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
                  model: {provider: openai, name: gpt-4o}
ai-models:
  - name: m1
    alias: m1
`)
	doc, warnings, err := revertYAML(t, in, Options{Strict: true})
	require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
	require.Empty(t, warnings, "want no warnings for aliasless targets that rely on ai-models for naming only")
	require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
	require.Equal(t, "m1", doc.Models[0].Name, "model name should come from ai-models")
	require.Empty(t, doc.Models[0].Config.Model.Alias, "synthetic ai-models alias should not be restored into model.alias")
}

func TestMCPLabelsPreferPluginTagsWithServiceFallback(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: team-a
    url: https://team-a.internal.example.com/mcp
    tags: [legacy:service]
    routes:
      - name: team-a-route
        paths: [/mcp/team-a]
        plugins:
          - name: ai-mcp-proxy
            tags: [aigw610:tools]
            config:
              mode: conversion-only
              tools:
                - name: team-a-get-report
                  description: Get a report
                  method: GET
                  path: /report
  - name: aggregate
    host: localhost
    routes:
      - name: aggregate-route
        paths: [/mcp/aggregate]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
              server:
                tag: aigw610:tools
  - name: fallback
    url: https://fallback.internal.example.com/mcp
    tags: [fallback:service]
    routes:
      - name: fallback-route
        paths: [/mcp/fallback]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: conversion-only
              tools:
                - name: fallback-tool
                  description: Fallback tool
                  method: GET
                  path: /fallback
`)

	doc, warnings, err := revertYAML(t, in, Options{Strict: true})
	require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
	require.Empty(t, warnings, "want no warnings")
	require.Len(t, doc.MCPServers, 3, "mcp servers = %+v", doc.MCPServers)

	byName := map[string]aigw.MCPServer{}
	for _, server := range doc.MCPServers {
		byName[server.Name] = server
	}

	require.Equal(t, aigw.Labels{"aigw610": "tools"}, byName["team-a"].Labels, "plugin tags should win")
	require.Equal(t, aigw.Labels{"fallback": "service"},
		byName["fallback"].Labels, "service tags should remain the fallback")
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
	require.NoError(t, err, "revert")
	require.Contains(t, strings.Join(warnings, "\n"), "no ai-models entry for alias",
		"want a no-ai-models-entry-for-alias warning")
}

func TestStrictModeMakesDropsFatal(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: orphan
    url: http://nowhere.invalid
`)
	_, warnings, err := revertYAML(t, in, Options{})
	require.NoError(t, err, "non-strict")
	require.Len(t, warnings, 1, "non-strict: want 1 warning")

	_, _, err = revertYAML(t, in, Options{Strict: true})
	require.Error(t, err, "strict: want a no-routes error")
	require.Contains(t, err.Error(), "no routes", "strict: want a no-routes error")
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
	require.NoError(t, err, "revert")
	require.Lenf(t, doc.Models, 1, "models = %+v; want one model", doc.Models)
	require.Equalf(t, []string{"generate"}, doc.Models[0].Capabilities,
		"models = %+v; want capability generate", doc.Models)
	require.Contains(t, strings.Join(warnings, "\n"), "defaulting to generate",
		"want a defaulting-to-generate warning")
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
	require.NoErrorf(t, err, "re-parse output:\n%s", out)
	return doc, warnings, nil
}
