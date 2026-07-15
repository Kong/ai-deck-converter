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
	require.Equal(t, []string{"region"}, dropped, "unknown gcp_environment key must be reported")
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
