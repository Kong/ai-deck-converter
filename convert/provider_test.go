package convert

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gperanich/ai-deck-converter/internal/aigw"
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
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("resolveAuth header mismatch (-want +got):\n%s", diff)
	}
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
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("resolveAuth param mismatch (-want +got):\n%s", diff)
	}
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
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("resolveAuth azure mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveAuthNilProvider(t *testing.T) {
	if got := resolveAuth(nil, nil); got != nil {
		t.Errorf("expected nil auth for nil provider, got %v", got)
	}
}

func TestMapOptionsAzureRenames(t *testing.T) {
	p := &aigw.Provider{Type: "azure", Config: aigw.ProviderConfig{Instance: "kong-az"}}
	got := mapOptions(map[string]any{
		"temperature":   0.5,
		"deployment_id": "gpt4o-dep",
		"api_version":   "2024-02-01",
	}, "azure", p)
	want := map[string]any{
		"temperature":         0.5,
		"azure_deployment_id": "gpt4o-dep",
		"azure_api_version":   "2024-02-01",
		"azure_instance":      "kong-az",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mapOptions mismatch (-want +got):\n%s", diff)
	}
}

func TestMapOptionsGeminiNesting(t *testing.T) {
	p := &aigw.Provider{Type: "vertex", Config: aigw.ProviderConfig{ProjectID: "kong-proj"}}
	got := mapOptions(map[string]any{
		"max_tokens":  4096,
		"location_id": "us-central1",
	}, "vertex", p)
	want := map[string]any{
		"max_tokens": 4096,
		"gemini": map[string]any{
			"project_id":  "kong-proj",
			"location_id": "us-central1",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mapOptions gemini mismatch (-want +got):\n%s", diff)
	}
}

func TestMapOptionsBedrockNesting(t *testing.T) {
	p := &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
		Type: "aws", AssumeRoleARN: "arn:role",
	}}}
	got := mapOptions(map[string]any{
		"max_tokens": 1024,
		"aws_region": "us-east-1",
	}, "bedrock", p)
	want := map[string]any{
		"max_tokens": 1024,
		"bedrock": map[string]any{
			"aws_region":          "us-east-1",
			"aws_assume_role_arn": "arn:role",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mapOptions bedrock mismatch (-want +got):\n%s", diff)
	}
}

func TestConvertWarnsUnsupportedCapability(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [image]
    formats: [{type: anthropic}]
    target_models:
      - name: claude
        provider: {name: anthropic-main}
        config: {type: anthropic}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/ai]}
      model: {}
providers:
  - type: anthropic
    name: anthropic-main
    config: {auth: {type: basic, headers: [{name: x-api-key, value: k}]}}
`)
	_, warnings, err := Convert(src, Options{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsSubstr(warnings, "no endpoint for capability") {
		t.Errorf("expected unsupported-capability warning, got %v", warnings)
	}
}
