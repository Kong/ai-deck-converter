package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestRevertMultiAliasValues merges the ai-proxy-advanced plugin copies a
// multi-alias fan-out produced back into one Model whose
// config.route.model.values lists every alias. The fan-out shape is only
// recognized when the ai-models entries carry the converter's group marker
// tag: N independently-authored config-identical models must stay separate.
func TestRevertMultiAliasValues(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths:
          - /ai/chat/completions
        methods:
          - POST
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
      balancer:
        algorithm: round-robin
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          logging:
            log_payloads: false
            log_statistics: true
          model:
            model_alias: '@kong/openai'
            name: gpt-5
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/openai'
  - name: ai-proxy-advanced
    config:
      balancer:
        algorithm: round-robin
      genai_category: text/generation
      llm_format: openai
      targets:
        - description: gpt-5
          logging:
            log_payloads: false
            log_statistics: true
          model:
            model_alias: '@kong/the-openai-model'
            name: gpt-5
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/the-openai-model'
ai_models:
  - name: '@kong/openai'
    tags:
      - ai-gateway-model-alias-group:@kong/openai
  - name: '@kong/the-openai-model'
    tags:
      - ai-gateway-model-alias-group:@kong/openai
`)

	doc, warnings := revertToDoc(t, src, Options{})
	require.Empty(t, warnings, "no warnings expected")
	require.Len(t, doc.Models, 1, "fan-out copies merge into one model")

	m := doc.Models[0]
	require.Equal(t, "@kong/openai", m.Name, "merged model is named by the first alias")
	require.Equal(t, []string{"@kong/openai", "@kong/the-openai-model"},
		m.Config.Route.Model.Values, "every alias lands in route.model.values")
	require.Len(t, m.TargetModels, 1, "one target")
}

func TestRevertMultiAliasValuesWithoutMarkerStaysSeparate(t *testing.T) {
	// Same shape as TestRevertMultiAliasValues but without the group marker
	// tag: hand-authored config-identical models must not be merged.
	src := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths:
          - /ai/chat/completions
        methods:
          - POST
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
      llm_format: openai
      targets:
        - model:
            model_alias: '@kong/model-a'
            name: gpt-5
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/model-a'
  - name: ai-proxy-advanced
    config:
      llm_format: openai
      targets:
        - model:
            model_alias: '@kong/model-b'
            name: gpt-5
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/model-b'
ai_models:
  - name: '@kong/model-a'
  - name: '@kong/model-b'
`)

	doc, warnings := revertToDoc(t, src, Options{})
	require.Empty(t, warnings, "no warnings expected")
	require.Len(t, doc.Models, 2, "unmarked identical models stay separate")

	values := map[string][]string{}
	for _, m := range doc.Models {
		values[m.Name] = m.Config.Route.Model.Values
	}
	require.Empty(t, values["@kong/model-a"], "alias equal to name needs no values")
	require.Empty(t, values["@kong/model-b"], "alias equal to name needs no values")
}

func TestRevertMultiAliasValuesMergesPoliciesAndLabels(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths:
          - /ai/chat/completions
        methods:
          - POST
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
      llm_format: openai
      targets:
        - model:
            model_alias: '@kong/gpt-4o'
            name: gpt-4o
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/gpt-4o'
  - name: ai-proxy-advanced
    config:
      llm_format: openai
      targets:
        - model:
            model_alias: '@kong/gpt-4o-alias'
            name: gpt-4o
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/gpt-4o-alias'
  - name: ai-prompt-guard
    config:
      allow_patterns:
        - ^safe
    route: openai-chat
    model: '@kong/gpt-4o'
  - name: ai-prompt-guard
    config:
      allow_patterns:
        - ^safe
    route: openai-chat
    model: '@kong/gpt-4o-alias'
  - name: acl
    config:
      allow:
        - premium-users
    route: openai-chat
    model: '@kong/gpt-4o'
  - name: acl
    config:
      allow:
        - premium-users
    route: openai-chat
    model: '@kong/gpt-4o-alias'
ai_models:
  - name: '@kong/gpt-4o'
    tags:
      - aigw/team:platform
      - ai-gateway-model-alias-group:@kong/gpt-4o
  - name: '@kong/gpt-4o-alias'
    tags:
      - aigw/team:platform
      - ai-gateway-model-alias-group:@kong/gpt-4o
`)

	opts := Options{LabelTagPrefix: "aigw/"}
	doc, warnings := revertToDoc(t, src, opts)
	require.Empty(t, warnings, "no warnings expected")
	require.Len(t, doc.Models, 1, "fan-out copies merge into one model")

	m := doc.Models[0]
	require.Equal(t, []string{"@kong/gpt-4o", "@kong/gpt-4o-alias"},
		m.Config.Route.Model.Values, "every alias lands in route.model.values")
	require.Equal(t, []string{"ai-prompt-guard"}, m.Policies,
		"guard plugins scoped to any merged FK fold into the model once")
	require.Equal(t, aigw.ACLs{Allow: []string{"premium-users"}}, m.Access.ACLs,
		"ACLs scoped to any merged FK fold into the model")
	require.Equal(t, aigw.Labels{"team": "platform"}, m.Labels,
		"label tags survive; the group marker tag is stripped")
}

// revertToDoc runs Revert and decodes the AI Gateway YAML for assertions.
func revertToDoc(t *testing.T, src []byte, opts Options) (*aigw.Document, []string) {
	t.Helper()
	out, warnings, err := Revert(src, opts)
	require.NoError(t, err, "revert")
	var doc aigw.Document
	require.NoError(t, yaml.Unmarshal(out, &doc), "unmarshal reverted document")
	return &doc, warnings
}

func TestRevertMultiAliasValuesMergesRoutelessACLS(t *testing.T) {
	// Route-less, model-only acl plugins (no route FK) are folded per merged
	// alias FK in finalizeModels; their allow/deny lists must merge across
	// aliases, not overwrite each other.
	src := []byte(`
_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat
        paths:
          - /ai/chat/completions
        methods:
          - POST
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
      llm_format: openai
      targets:
        - model:
            model_alias: '@kong/gpt-4o'
            name: gpt-4o
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/gpt-4o'
  - name: ai-proxy-advanced
    config:
      llm_format: openai
      targets:
        - model:
            model_alias: '@kong/gpt-4o-alias'
            name: gpt-4o
            provider: openai
          route_type: llm/v1/chat
    route: openai-chat
    model: '@kong/gpt-4o-alias'
  - name: acl
    config:
      allow:
        - premium-users
    model: '@kong/gpt-4o'
  - name: acl
    config:
      allow:
        - enterprise-users
      deny:
        - suspended-users
    model: '@kong/gpt-4o-alias'
ai_models:
  - name: '@kong/gpt-4o'
    tags:
      - ai-gateway-model-alias-group:@kong/gpt-4o
  - name: '@kong/gpt-4o-alias'
    tags:
      - ai-gateway-model-alias-group:@kong/gpt-4o
`)

	doc, warnings := revertToDoc(t, src, Options{})
	require.Empty(t, warnings, "no warnings expected")
	require.Len(t, doc.Models, 1, "fan-out copies merge into one model")

	m := doc.Models[0]
	require.Equal(t, aigw.ACLs{
		Allow: []string{"premium-users", "enterprise-users"},
		Deny:  []string{"suspended-users"},
	}, m.Access.ACLs,
		"route-less acl plugins from every merged alias merge, none is dropped")
}
