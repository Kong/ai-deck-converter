package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
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
	got := mapOptions(map[string]any{
		"temperature":   0.5,
		"deployment_id": "gpt4o-dep",
		"api_version":   "2024-02-01",
	}, "azure", "gpt-4o", p, false, aimap.LatestVersion)
	want := map[string]any{
		"temperature":         0.5,
		"azure_deployment_id": "gpt4o-dep",
		"azure_api_version":   "2024-02-01",
		"azure_instance":      "kong-az",
	}
	require.Equal(t, want, got, "mapOptions mismatch")
}

func TestMapOptionsGeminiNesting(t *testing.T) {
	p := &aigw.Provider{Type: "gemini", Config: aigw.ProviderConfig{}}
	got := mapOptions(map[string]any{
		"max_tokens": 4096,
		"gcp_environment": map[string]any{
			"project_id":   "kong-proj",
			"location_id":  "us-central1",
			"api_endpoint": "https://us-central1-aiplatform.googleapis.com",
		},
	}, "gemini", "gemini-2.5-pro", p, false, aimap.LatestVersion)
	want := map[string]any{
		"max_tokens": 4096,
		"gemini": map[string]any{
			"project_id":   "kong-proj",
			"location_id":  "us-central1",
			"api_endpoint": "https://us-central1-aiplatform.googleapis.com",
		},
	}
	require.Equal(t, want, got, "mapOptions gemini mismatch")
}

func TestMapOptionsGeminiEndpointBelowGate(t *testing.T) {
	below := aimap.Version{Major: 2, Minor: 3}
	env := func(location string) map[string]any {
		return map[string]any{"project_id": "my-project", "location_id": location}
	}
	withEndpoint := func(location, endpoint string) map[string]any {
		return map[string]any{
			"api_endpoint": endpoint, "project_id": "my-project", "location_id": location,
		}
	}
	tests := []struct {
		name string
		env  map[string]any
		want map[string]any
	}{
		{"global", env("global"), withEndpoint("global", "aiplatform.googleapis.com")},
		{"us multi-region", env("us"), withEndpoint("us", "aiplatform.us.rep.googleapis.com")},
		{"eu multi-region", env("eu"), withEndpoint("eu", "aiplatform.eu.rep.googleapis.com")},
		{
			"region", env("us-central1"),
			withEndpoint("us-central1", "us-central1-aiplatform.googleapis.com"),
		},
		{
			"empty endpoint",
			map[string]any{"api_endpoint": "", "project_id": "my-project", "location_id": "asia-northeast1"},
			withEndpoint("asia-northeast1", "asia-northeast1-aiplatform.googleapis.com"),
		},
		{
			"null endpoint",
			map[string]any{
				"api_endpoint": nil, "project_id": "my-project", "location_id": "southamerica-east1",
			},
			withEndpoint("southamerica-east1", "southamerica-east1-aiplatform.googleapis.com"),
		},
		{
			"explicit endpoint",
			map[string]any{
				"api_endpoint": "https://gemini.internal.example.com",
				"project_id":   "my-project",
				"location_id":  "europe-west4",
			},
			map[string]any{
				"api_endpoint": "https://gemini.internal.example.com",
				"project_id":   "my-project",
				"location_id":  "europe-west4",
			},
		},
		{
			"no location",
			map[string]any{"project_id": "my-project"},
			map[string]any{"project_id": "my-project"},
		},
		{
			"no project",
			map[string]any{"location_id": "global"},
			map[string]any{"location_id": "global"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapOptions(
				map[string]any{"gcp_environment": tt.env}, "gemini", "gemini-2.5-pro", nil, false, below)
			require.Equal(t, map[string]any{"gemini": tt.want}, got)
		})
	}
}

func TestMapOptionsGeminiEndpointAtGate(t *testing.T) {
	got := mapOptions(map[string]any{"gcp_environment": map[string]any{
		"project_id": "my-project", "location_id": "us",
	}}, "gemini", "gemini-2.5-pro", nil, false, aimap.LatestVersion)
	require.Equal(t, map[string]any{"gemini": map[string]any{
		"location_id": "us", "project_id": "my-project",
	}}, got)
}

func TestMapOptionsBedrockNesting(t *testing.T) {
	p := &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
		Type: "aws", AssumeRoleARN: "arn:role",
	}}}
	got := mapOptions(map[string]any{
		"max_tokens": 1024,
		"region":     "us-east-1",
	}, "bedrock", "anthropic.claude-3-5-sonnet", p, false, aimap.LatestVersion)
	want := map[string]any{
		"max_tokens":        1024,
		"anthropic_version": "bedrock-2023-05-31",
		"bedrock": map[string]any{
			"aws_region":          "us-east-1",
			"aws_assume_role_arn": "arn:role",
		},
	}
	require.Equal(t, want, got, "mapOptions bedrock mismatch")
}

func TestMapOptionsBedrockNonAnthropicDoesNotDefaultAnthropicVersion(t *testing.T) {
	got := mapOptions(nil, "bedrock", "amazon.titan-embed-text-v2:0",
		&aigw.Provider{Type: "bedrock"}, false, aimap.LatestVersion)
	require.Nil(t, got)
}

func TestLowerEmbeddingsModelBedrockNonAnthropicDoesNotDefaultAnthropicVersion(t *testing.T) {
	embeddings := map[string]any{
		"model": map[string]any{
			"name":   "amazon.titan-embed-text-v2:0",
			"config": map[string]any{"type": "bedrock"},
		},
	}

	lowerEmbeddingsModel(embeddings, &aigw.Provider{Type: "bedrock"}, aimap.LatestVersion)
	model := embeddings["model"].(map[string]any)
	require.Equal(t, "bedrock", model["provider"])
	require.NotContains(t, model, "options")
}

func TestConvertRejectsUnsupportedCapability(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [rerank]
    formats: [{type: openai}]
    targets:
      - name: cohere.rerank-v3-5:0
        provider: bedrock-main
        config: {type: bedrock}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/ai]}
      model: {}
model_providers:
  - type: bedrock
    name: bedrock-main
    config: {auth: {type: basic, headers: [{name: x-api-key, value: k}]}}
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "m1", "capabilities[0]")
	require.Contains(t, issue.Reason, `capability "rerank" is not supported with llm_format "openai"`)
	require.Empty(t, result.Output.AIModels, "the model is left out")
}

func TestResolveEmbeddingsGeminiEndpointInvalidLocation(t *testing.T) {
	src := []byte(`
models:
  - name: semantic-lb
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config:
      route: {paths: [/ai]}
      balancer:
        algorithm: semantic
        embeddings:
          provider: gemini-prod
          model:
            name: text-embedding-004
            config:
              type: gemini
              gcp_environment:
                project_id: my-project
                location_id: evil.example.com/x
    targets:
      - name: gpt-4o
        provider: openai-main
        config: {type: openai}
model_providers:
  - name: openai-main
    type: openai
  - name: gemini-prod
    type: gemini
`)
	result, err := Convert(src, Options{MinRuntimeVersion: "2.3"})
	require.NoError(t, err, "convert")
	require.Empty(t, result.Output.AIModels, "the model is left out")
	requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "semantic-lb",
		"config.balancer.embeddings.model.config.gcp_environment.location_id")
}

// A passthrough target forwards the client's request unchanged, so the plugin must not
// default the version headers it would otherwise set when building the upstream request.
func TestMapOptionsPassthroughDoesNotDefaultAnthropicVersion(t *testing.T) {
	anthropic := &aigw.Provider{Type: "anthropic"}
	require.Nil(t, mapOptions(nil, "anthropic", "claude-sonnet-4-5", anthropic, true, aimap.LatestVersion))
	require.Equal(t,
		map[string]any{"anthropic_version": "2023-06-01"},
		mapOptions(nil, "anthropic", "claude-sonnet-4-5", anthropic, false, aimap.LatestVersion))

	bedrock := &aigw.Provider{Type: "bedrock"}
	require.Nil(t, mapOptions(nil, "bedrock", "anthropic.claude-3-5-sonnet", bedrock, true, aimap.LatestVersion))

	// An explicitly authored version is the client's business either way: it is passed
	// through, not defaulted, so passthrough does not drop it.
	require.Equal(t,
		map[string]any{"anthropic_version": "2024-01-01"},
		mapOptions(map[string]any{"version": "2024-01-01"}, "anthropic", "claude-sonnet-4-5",
			anthropic, true, aimap.LatestVersion))
}

func TestClientAuthOverride(t *testing.T) {
	basic := aigw.ProviderConfig{Auth: aigw.ProviderAuth{Type: "basic"}}
	tests := []struct {
		name     string
		provider *aigw.Provider
		want     *bool
	}{
		{"bedrock client auth", &aigw.Provider{Type: "bedrock", Config: basic}, ptrBool(true)},
		{"bedrock header", &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
			Type: "basic", Headers: []aigw.AuthHeader{{Name: "Authorization", Value: "Bearer k"}},
		}}}, nil},
		{"bedrock param", &aigw.Provider{Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{
			Type: "basic", Params: []aigw.AuthParam{{Name: "key", Value: "abc"}},
		}}}, nil},
		{"bedrock aws", &aigw.Provider{
			Type: "bedrock", Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{Type: "aws"}},
		}, nil},
		{"bedrock token vault", &aigw.Provider{Type: "bedrock", Config: basic, TokenVault: &aigw.TokenVaultConfig{}}, nil},
		{"openai client auth", &aigw.Provider{Type: "openai", Config: basic}, nil},
		{"nil provider", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, clientAuthOverride(tc.provider))
		})
	}
}

func TestResolveEmbeddingsBedrockClientAuth(t *testing.T) {
	c := newConverter(nil, Options{})
	c.providers["bedrock-client"] = &aigw.Provider{
		Type: "bedrock", Name: "bedrock-client",
		Config: aigw.ProviderConfig{Auth: aigw.ProviderAuth{Type: "basic"}},
	}
	raw := map[string]any{
		"provider": "bedrock-client",
		"auth":     map[string]any{"allow_override": false},
		"model": map[string]any{
			"name":   "amazon.titan-embed-text-v2:0",
			"config": map[string]any{"type": "bedrock"},
		},
	}

	got, err := c.resolveEmbeddings(issueSource("model", "m"), raw)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"allow_override": true}, got.(map[string]any)["auth"])
	require.Equal(t, false, raw["auth"].(map[string]any)["allow_override"], "source must stay unchanged")
}
