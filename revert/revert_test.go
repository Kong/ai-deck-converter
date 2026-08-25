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

func TestRevertAgentAuthStrategyPlugin(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
services:
  - name: protected-agent
    url: https://example.test
    routes:
      - name: protected-agent-route
        paths: [/protected-agent]
        plugins:
          - name: key-auth
            config:
              anonymous: anonymous
              key_names: [apikey]
          - name: acl
            config:
              allow: [allowed-group]
              include_consumer_groups: true
`)

	out, _, err := Revert(src, Options{})
	require.NoError(t, err)
	require.Contains(t, string(out), "auth_strategies:\n        - key-auth-1")
	require.Contains(t, string(out), "- key-auth-1")
	require.Contains(t, string(out), "auth_strategies:\n  - type: key-auth")
	require.NotContains(t, string(out), "policies:\n")
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
	require.Lenf(t, doc.ModelProviders, 2, "identical auth must dedupe: %+v", doc.ModelProviders)
	require.Equal(t, "openai-env", doc.ModelProviders[0].Name, "first provider name")
	require.Equal(t, "openai-other", doc.ModelProviders[1].Name, "second provider name")
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
ai_models:
  - name: m1
    alias: m1
`)
	doc, warnings, err := revertYAML(t, in, Options{Strict: true})
	require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
	require.Empty(t, warnings, "want no warnings for aliasless targets that rely on ai-models for naming only")
	require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
	require.Equal(t, "m1", doc.Models[0].Name, "model name should come from ai-models")
	require.Empty(t, doc.Models[0].Config.Route.Model.Values,
		"synthetic ai-models alias should not be restored into model.alias")
}

func TestMultiAliasMerge(t *testing.T) {
	t.Run("identical plugins merge into one model with combined values", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings, "want no warnings merging identical alias plugins")
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.Equal(t, []string{"alias-a", "alias-b"}, doc.Models[0].Config.Route.Model.Values)
		require.Empty(t, doc.Models[0].Labels, "the marker tag must not leak into labels")
	})

	t.Run("identical plugins without the marker tag do not merge (KOKO-4291 regression)", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
  - name: alias-b
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 2, "models = %+v; two independently-authored, config-identical "+
			"models sharing a route must stay separate without the marker tag", doc.Models)
	})

	t.Run("marker matches but targets differ: do not merge", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5-mini
          model: {model_alias: alias-b, name: gpt-5-mini, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 2, "models = %+v; the marker tag matches but the targets don't, "+
			"so the content-shape check must still refuse to merge", doc.Models)
		require.Equal(t, "alias-a", doc.Models[0].Name)
		require.Empty(t, doc.Models[0].Config.Route.Model.Values, "name already equals its own alias implicitly")
		require.Equal(t, "alias-b", doc.Models[1].Name)
		require.Empty(t, doc.Models[1].Config.Route.Model.Values, "name already equals its own alias implicitly")
	})

	t.Run("merge also dedupes guard plugins without orphan-FK warnings", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
  - name: ai-prompt-guard
    config:
      allow_patterns: [^safe]
    route: openai-chat
    model: alias-a
  - name: ai-prompt-guard
    config:
      allow_patterns: [^safe]
    route: openai-chat
    model: alias-b
  - name: acl
    config:
      allow: [premium-users]
      include_consumer_groups: true
    route: openai-chat
    model: alias-a
  - name: acl
    config:
      allow: [premium-users]
      include_consumer_groups: true
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings, "want no orphan-FK warnings for merged-away aliases")
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.Equal(t, []string{"alias-a", "alias-b"}, doc.Models[0].Config.Route.Model.Values)
		require.Equal(t, []string{"ai-prompt-guard"}, doc.Models[0].Policies)
		require.Equal(t, []string{"premium-users"}, doc.Models[0].Access.ACLs.Allow)
		require.Len(t, doc.Policies, 1, "top-level policies = %+v", doc.Policies)
	})

	t.Run("merge does not drop a guard plugin scoped only to a non-canonical alias", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
  - name: ai-prompt-guard
    config:
      allow_patterns: [^safe]
    route: openai-chat
    model: alias-a
  - name: rate-limiting
    config:
      minute: 100
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.ElementsMatch(t, []string{"ai-prompt-guard", "rate-limiting"}, doc.Models[0].Policies,
			"alias-b's own guard plugin, scoped only to it (not alias-a), must not be dropped by the merge")
	})

	t.Run("merge does not drop an auth-strategy plugin scoped only to a non-canonical alias", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
  - name: openid-connect
    config:
      anonymous: anonymous
      issuer: https://dev-123456.okta.com/oauth2/default
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.Equal(t, []string{"openid-connect-1"}, doc.Models[0].Access.AuthStrategies,
			"alias-b's own auth-strategy plugin, scoped only to it, must not be dropped by the merge")
	})

	t.Run("merge unions ACL allow/deny lists instead of dropping one alias's rules", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
  - name: acl
    config:
      allow: [premium-users]
      deny: [banned-a]
      include_consumer_groups: true
    route: openai-chat
    model: alias-a
  - name: acl
    config:
      allow: [beta-users]
      deny: [banned-b]
      include_consumer_groups: true
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.ElementsMatch(t, []string{"premium-users", "beta-users"}, doc.Models[0].Access.ACLs.Allow,
			"alias-b's own allow rule must not be dropped in favor of alias-a's")
		require.ElementsMatch(t, []string{"banned-a", "banned-b"}, doc.Models[0].Access.ACLs.Deny,
			"alias-b's own deny rule must not be dropped in favor of alias-a's")
	})

	t.Run("three or more aliases merge into one model, in order", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-c, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-c
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-c
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.Equal(t, []string{"alias-a", "alias-b", "alias-c"}, doc.Models[0].Config.Route.Model.Values)
	})

	t.Run("two independent multi-alias groups sharing one route merge separately", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      max_request_body_size: 8388608
      sources:
        - body_path: model
          source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: a1, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: a1
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: a2, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: a2
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-6
          model: {model_alias: b1, name: gpt-6, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: b1
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-6
          model: {model_alias: b2, name: gpt-6, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: b2
ai_models:
  - name: a1
    tags: [ai-gateway-model-alias-group:group-a]
  - name: a2
    tags: [ai-gateway-model-alias-group:group-a]
  - name: b1
    tags: [ai-gateway-model-alias-group:group-b]
  - name: b2
    tags: [ai-gateway-model-alias-group:group-b]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 2, "models = %+v; each group must merge only within itself", doc.Models)
		require.Equal(t, []string{"a1", "a2"}, doc.Models[0].Config.Route.Model.Values)
		require.Equal(t, []string{"b1", "b2"}, doc.Models[1].Config.Route.Model.Values)
	})

	t.Run("merges under the legacy flat ai-model-selector schema too", func(t *testing.T) {
		in := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths: [/ai/chat/completions]
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector
    config:
      body_path: model
      max_request_body_size: 8388608
      source: body
    route: openai-chat
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-a, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-a
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          model: {model_alias: alias-b, name: gpt-5, provider: openai}
          route_type: llm/v1/chat
    route: openai-chat
    model: alias-b
ai_models:
  - name: alias-a
    tags: [ai-gateway-model-alias-group:source-model]
  - name: alias-b
    tags: [ai-gateway-model-alias-group:source-model]
`)
		doc, warnings, err := revertYAML(t, in, Options{Strict: true})
		require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
		require.Empty(t, warnings)
		require.Len(t, doc.Models, 1, "models = %+v", doc.Models)
		require.Equal(t, []string{"alias-a", "alias-b"}, doc.Models[0].Config.Route.Model.Values)
	})
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
            tags: [team:a]
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

	require.Equal(t, aigw.Labels{"team": "a"}, byName["team-a"].Labels, "plugin tags should win")
	require.Equal(t, aigw.Labels{"fallback": "service"},
		byName["fallback"].Labels, "service tags should remain the fallback")
	require.Equal(t, "aigw610:tools", byName["aggregate"].Config.Server["tag"],
		"listener server.tag is carried through as-is")
}

// TestRevertMCPListenerReconstructsSources asserts that the listener/source
// relationship the forward converter encodes as tags is lifted back into the
// listener's config.sources: the bucket tag on each source plugin selects it
// into the listener's sources and is stripped from the source's labels.
func TestRevertMCPListenerReconstructsSources(t *testing.T) {
	in := []byte(`
_format_version: "3.0"
services:
  - name: team-a
    host: localhost
    routes:
      - name: team-a-route
        paths: [/mcp/team-a]
        plugins:
          - name: ai-mcp-proxy
            tags: [mcp-listener:aggregate-id, env:prod]
            config:
              mode: conversion-only
              tools:
                - {name: team-a-report, description: Get a report, method: GET, path: /report}
  - name: team-b
    host: localhost
    routes:
      - name: team-b-route
        paths: [/mcp/team-b]
        plugins:
          - name: ai-mcp-proxy
            tags: [mcp-listener:aggregate-id]
            config:
              mode: conversion-only
              tools:
                - {name: team-b-report, description: Get a report, method: GET, path: /report}
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
                tag: mcp-listener:aggregate-id
`)

	doc, warnings, err := revertYAML(t, in, Options{Strict: true})
	require.NoErrorf(t, err, "strict revert (warnings: %v)", warnings)
	require.Empty(t, warnings, "want no warnings")

	byName := map[string]aigw.MCPServer{}
	for _, server := range doc.MCPServers {
		byName[server.Name] = server
	}

	require.Equal(t, []string{"team-a", "team-b"}, byName["aggregate"].Config.Sources,
		"listener sources reconstructed from bucket tags, sorted")
	require.Equal(t, aigw.Labels{"env": "prod"}, byName["team-a"].Labels,
		"bucket tag stripped from source labels, genuine labels kept")
	require.Empty(t, byName["team-b"].Labels, "bucket-only tags leave no labels")
	require.Empty(t, byName["team-a"].Config.Sources, "sources are populated only on listeners")
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
ai_models:
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
