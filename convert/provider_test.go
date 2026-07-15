package convert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
)

func ptrBool(b bool) *bool { return &b }

func TestResolveAuthHeader(t *testing.T) {
	p := &aigw.Provider{Type: "openai", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
		Type:    "basic",
		Headers: []aigw.AuthHeader{{Name: "Authorization", Value: "Bearer k"}},
	}}}
	got := resolveAuth(p, ptrBool(true))
	want := map[string]any{
		"header_name":    "Authorization",
		"header_value":   "Bearer k",
		"allow_override": true,
	}
	require.Equal(t, want, got, "resolveAuth header mismatch")
}

func TestResolveAuthParam(t *testing.T) {
	p := &aigw.Provider{Type: "gemini", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
		Type:   "basic",
		Params: []aigw.AuthParam{{Name: "key", Value: "abc", Location: "query"}},
	}}}
	got := resolveAuth(p, nil)
	want := map[string]any{
		"param_name":     "key",
		"param_value":    "abc",
		"param_location": "query",
	}
	require.Equal(t, want, got, "resolveAuth param mismatch")
}

func TestResolveAuthCloud(t *testing.T) {
	p := &aigw.Provider{Type: "azure", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
		Type:               "azure",
		ClientID:           "cid",
		TenantID:           "tid",
		UseManagedIdentity: ptrBool(true),
	}}}
	got := resolveAuth(p, nil)
	want := map[string]any{
		"azure_client_id":            "cid",
		"azure_tenant_id":            "tid",
		"azure_use_managed_identity": true,
	}
	require.Equal(t, want, got, "resolveAuth azure mismatch")
}

func TestResolveAuthNilProvider(t *testing.T) {
	require.Nil(t, resolveAuth(nil, nil), "expected nil auth for nil provider")
}

func TestMapOptionsAzureRenames(t *testing.T) {
	p := &aigw.Provider{Type: "azure", Config: aigw.ProviderConfig{Instance: "kong-az"}}
	got, dropped := mapOptions(map[string]any{
		"temperature":   0.5,
		"deployment_id": "gpt4o-dep",
		"api_version":   "2024-02-01",
	}, "azure", "gpt-4o", p)
	want := map[string]any{
		"temperature":         0.5,
		"azure_deployment_id": "gpt4o-dep",
		"azure_api_version":   "2024-02-01",
		"azure_instance":      "kong-az",
	}
	require.Equal(t, want, got, "mapOptions mismatch")
	require.Empty(t, dropped, "no keys should be dropped")
}

func TestMapOptionsDropsUnknownKeys(t *testing.T) {
	p := &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{Type: "aws"}}}
	got, dropped := mapOptions(map[string]any{
		"max_tokens":           2048,
		"region":               "eu-central-1",
		"guardrail_identifier": "gr-abc123",
		"guardrail_version":    "3",
		"trace":                "enabled",
	}, "bedrock", "anthropic.claude-sonnet-4-5", p)
	want := map[string]any{
		"max_tokens":        2048,
		"anthropic_version": "bedrock-2023-05-31",
		"bedrock":           map[string]any{"aws_region": "eu-central-1"},
	}
	require.Equal(t, want, got, "unknown keys must not pass through flat")
	require.Equal(t, []string{"guardrail_identifier", "guardrail_version", "trace"}, dropped,
		"unknown keys must be reported sorted for a deterministic warning")
}

func TestMapOptionsValidatesRawBedrockNestedKey(t *testing.T) {
	// A hand-author may write the DP's own nested shape directly (`bedrock:
	// {...}`) instead of the AI Gateway's flat keys. mapOptions must accept it,
	// but validate each sub-field against the real DP schema
	// (kong/llm/schemas/options.lua bedrock_options_schema) individually:
	// known sub-fields pass through nested, unknown ones are dropped and
	// reported rather than sinking the whole block (AG-1246 follow-up).
	p := &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{Type: "aws"}}}
	got, dropped := mapOptions(map[string]any{
		"max_tokens": 1024,
		"bedrock": map[string]any{
			"aws_region":        "eu-central-1",
			"unvalidated_field": "whatever",
		},
	}, "bedrock", "anthropic.claude-3-5-sonnet", p)
	want := map[string]any{
		"max_tokens":        1024,
		"anthropic_version": "bedrock-2023-05-31",
		"bedrock":           map[string]any{"aws_region": "eu-central-1"},
	}
	require.Equal(t, want, got, "known bedrock sub-fields must pass through nested")
	require.Equal(t, []string{"bedrock.unvalidated_field"}, dropped,
		"unknown bedrock sub-field must be reported as dropped")
}

func TestMapOptionsValidatesRawGeminiNestedKey(t *testing.T) {
	p := &aigw.Provider{Type: "vertex"}
	got, dropped := mapOptions(map[string]any{
		"gemini": map[string]any{
			"project_id": "kong-proj",
			"region":     "us-central1", // not a real gemini sub-field
		},
	}, "vertex", "gemini-2.5-pro", p)
	want := map[string]any{
		"gemini": map[string]any{"project_id": "kong-proj"},
	}
	require.Equal(t, want, got, "known gemini sub-fields must pass through nested")
	require.Equal(t, []string{"gemini.region"}, dropped, "unknown gemini sub-field must be reported as dropped")
}

func TestMapOptionsValidatesRawSimpleNestedProviderKeys(t *testing.T) {
	// cohere, huggingface, databricks, dashscope, kimi have no rename/hoist of
	// their own: their AI-Gateway-flat key names are identical to the DP's
	// nested sub-field names, so the same per-provider allowlist gates both.
	cases := []struct {
		providerType string
		nested       map[string]any
		wantNested   map[string]any
		wantDropped  []string
	}{
		{
			"cohere",
			map[string]any{"embedding_input_type": "search_document", "bogus": true},
			map[string]any{"embedding_input_type": "search_document"},
			[]string{"cohere.bogus"},
		},
		{
			"huggingface",
			map[string]any{"use_cache": true, "bogus": true},
			map[string]any{"use_cache": true},
			[]string{"huggingface.bogus"},
		},
		{
			"databricks",
			map[string]any{"workspace_instance_id": "dbc-a1b2c3d4", "bogus": true},
			map[string]any{"workspace_instance_id": "dbc-a1b2c3d4"},
			[]string{"databricks.bogus"},
		},
		{
			"dashscope",
			map[string]any{"international": true, "bogus": true},
			map[string]any{"international": true},
			[]string{"dashscope.bogus"},
		},
		{
			"kimi",
			map[string]any{"international": false, "bogus": true},
			map[string]any{"international": false},
			[]string{"kimi.bogus"},
		},
	}
	for _, tc := range cases {
		got, dropped := mapOptions(map[string]any{tc.providerType: tc.nested}, tc.providerType, "m", nil)
		require.Equalf(t, map[string]any{tc.providerType: tc.wantNested}, got, "provider %s", tc.providerType)
		require.Equalf(t, tc.wantDropped, dropped, "provider %s", tc.providerType)
	}
}

func TestMapOptionsDropsRawAzureNestedKeyEntirely(t *testing.T) {
	// Unlike bedrock/gemini/cohere/..., "azure" is not a nested field of the
	// ai-proxy-advanced target's model.options schema at all (only the flat
	// azure_instance/azure_api_version/azure_deployment_id are) — a raw nested
	// "azure" key must be dropped wholesale regardless of its sub-fields, not
	// validated against a per-subkey allowlist.
	got, dropped := mapOptions(map[string]any{
		"azure": map[string]any{"instance": "kong-az"},
	}, "azure", "gpt-4o", &aigw.Provider{Type: "azure"})
	require.Empty(t, got, "raw \"azure\" input key is not part of the target options schema")
	require.Equal(t, []string{"azure"}, dropped, "the whole azure key must be reported as dropped")
}

func TestMapOptionsDropsMalformedNestedBlockWithWarning(t *testing.T) {
	// A nested-provider-record key whose value isn't a map (e.g. a typo'd
	// `bedrock: "eu-central-1"` instead of `bedrock: {aws_region: ...}`) must
	// still be reported as dropped, not silently discarded — dropping it with
	// no trace even under -strict would defeat the whole point of this warning
	// mechanism.
	got, dropped := mapOptions(map[string]any{
		"max_tokens": 1024,
		"bedrock":    "eu-central-1",
	}, "bedrock", "anthropic.claude-3-5-sonnet", nil)
	require.Equal(t, map[string]any{"max_tokens": 1024}, got, "malformed nested block must not appear in output")
	require.Equal(t, []string{"bedrock"}, dropped, "malformed nested block must be reported as dropped")
}

func TestMapOptionsDropsUnknownGCPEnvironmentKeys(t *testing.T) {
	p := &aigw.Provider{Type: "vertex"}
	got, dropped := mapOptions(map[string]any{
		"gcp_environment": map[string]any{
			"project_id": "kong-proj",
			"region":     "us-central1",
		},
	}, "vertex", "gemini-2.5-pro", p)
	want := map[string]any{
		"gemini": map[string]any{"project_id": "kong-proj"},
	}
	require.Equal(t, want, got, "unknown gcp_environment keys must not pass through flat")
	require.Equal(t, []string{"gemini.region"}, dropped,
		"unknown gcp_environment key must be reported with the gemini.<key> prefix, "+
			"consistent with mergeNestedBlock's format")
}

func TestMapOptionsGeminiNesting(t *testing.T) {
	p := &aigw.Provider{Type: "vertex", Config: aigw.ProviderConfig{}}
	got, dropped := mapOptions(map[string]any{
		"max_tokens": 4096,
		"gcp_environment": map[string]any{
			"project_id":   "kong-proj",
			"location_id":  "us-central1",
			"api_endpoint": "https://us-central1-aiplatform.googleapis.com",
		},
	}, "vertex", "gemini-2.5-pro", p)
	want := map[string]any{
		"max_tokens": 4096,
		"gemini": map[string]any{
			"project_id":   "kong-proj",
			"location_id":  "us-central1",
			"api_endpoint": "https://us-central1-aiplatform.googleapis.com",
		},
	}
	require.Equal(t, want, got, "mapOptions gemini mismatch")
	require.Empty(t, dropped, "no keys should be dropped")
}

func TestMapOptionsBedrockNesting(t *testing.T) {
	p := &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
		Type: "aws", AssumeRoleARN: "arn:role",
	}}}
	got, dropped := mapOptions(map[string]any{
		"max_tokens": 1024,
		"region":     "us-east-1",
	}, "bedrock", "anthropic.claude-3-5-sonnet", p)
	want := map[string]any{
		"max_tokens":        1024,
		"anthropic_version": "bedrock-2023-05-31",
		"bedrock": map[string]any{
			"aws_region":          "us-east-1",
			"aws_assume_role_arn": "arn:role",
		},
	}
	require.Equal(t, want, got, "mapOptions bedrock mismatch")
	require.Empty(t, dropped, "no keys should be dropped")
}

func TestMapOptionsBedrockNonAnthropicDoesNotDefaultAnthropicVersion(t *testing.T) {
	got, dropped := mapOptions(nil, "bedrock", "amazon.titan-embed-text-v2:0", &aigw.Provider{Type: "bedrock"})
	require.Nil(t, got)
	require.Empty(t, dropped, "no keys should be dropped")
}

func TestLowerEmbeddingsModelBedrockNonAnthropicDoesNotDefaultAnthropicVersion(t *testing.T) {
	embeddings := map[string]any{
		"model": map[string]any{
			"name":   "amazon.titan-embed-text-v2:0",
			"config": map[string]any{"type": "bedrock"},
		},
	}

	lowerEmbeddingsModel(embeddings, &aigw.Provider{Type: "bedrock"})
	model := embeddings["model"].(map[string]any)
	require.Equal(t, "bedrock", model["provider"])
	require.NotContains(t, model, "options")
}

func TestConvertWarnsUnsupportedCapability(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [image]
    formats: [{type: anthropic}]
    targets:
      - name: claude
        provider: anthropic-main
        config: {type: anthropic}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/ai]}
      model: {}
model_providers:
  - type: anthropic
    name: anthropic-main
    config: {auth: {type: basic, headers: [{name: x-api-key, value: k}]}}
`)
	_, warnings, err := Convert(src, Options{})
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "no endpoint for capability",
		"expected unsupported-capability warning")
}
