package aigw

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const sampleDoc = `
models:
  - type: model
    display_name: My GPT model
    name: my-gpt
    capabilities: [generate]
    formats:
      - type: openai
    targets:
      - name: gpt-4o
        weight: 100
        provider: openai-main
        config:
          type: openai
          temperature: 0.7
          max_tokens: 512
    policies: [pii-sanitizer]
    acls:
      allow: [dev-users]
      deny: []
    config:
      route:
        name: gpt-route
        paths: [/chat]
        methods: [POST]
        model:
          values: [my-gpt]
model_providers:
  - type: openai
    display_name: OpenAI Main
    name: openai-main
    config:
      auth:
        type: basic
        headers:
          - name: Authorization
            value: "{vault://env/openai-key}"
consumers:
  - name: gregs-dev
    type: api-key
    consumer_groups: [dev-users]
    policies: []
    credentials:
      - name: gregs-key
        type: api-key
        api_key: secret123
vaults:
  - type: env
    name: my-env
    config:
      prefix: SECRET_
`

func TestParseEnvelope(t *testing.T) {
	doc, err := Parse([]byte(sampleDoc))
	require.NoError(t, err, "parse")
	require.Len(t, doc.Models, 1, "want 1 model")
	m := doc.Models[0]
	require.Equal(t, "my-gpt", m.Name, "model name")
	require.Equal(t, "model", m.Type, "model type")
	require.Len(t, m.TargetModels, 1, "want 1 target")
	tm := m.TargetModels[0]
	require.Equal(t, "openai-main", tm.Provider, "target provider")
	require.Equal(t, "openai", tm.Config.Type, "target config type")
	require.NotContains(t, tm.Config.Options, "type", "type should be stripped from options")
	require.Equal(t, 0.7, tm.Config.Options["temperature"], "temperature") //nolint:testifylint
	require.Equal(t, "gpt-route", m.Config.Route.Name, "route name")
	require.Equal(t, []string{"my-gpt"}, m.Config.Route.Model.Values, "model alias")
	require.Len(t, doc.ModelProviders, 1, "provider not parsed")
	require.Equal(t, "Authorization", doc.ModelProviders[0].Config.Auth.Headers[0].Name, "provider auth not parsed")
	require.Len(t, doc.Consumers, 1, "consumer not parsed")
	require.Len(t, doc.Consumers[0].Credentials, 1, "credential not parsed")
	require.Len(t, doc.Vaults, 1, "vault not parsed")
	require.Equal(t, "env", doc.Vaults[0].Type, "vault type")
}

// TestParseAuthStrategies covers the top-level auth_strategies key.
func TestParseAuthStrategies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		names []string
	}{
		{
			name:  "auth_strategies key",
			src:   "auth_strategies:\n  - name: okta-oidc\n    type: openid-connect\n",
			names: []string{"okta-oidc"},
		},
		{
			name:  "no key",
			src:   "models: []\n",
			names: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.src))
			require.NoError(t, err)
			var got []string
			for _, s := range doc.AuthStrategies {
				got = append(got, s.Name)
			}
			require.Equal(t, tc.names, got)
		})
	}
}

// TestParseAccessAuthStrategyRefs covers the access.auth_strategies list on
// models, agents and MCP servers.
func TestParseAccessAuthStrategyRefs(t *testing.T) {
	doc, err := Parse([]byte(`
models:
  - name: m
    access:
      auth_strategies: [okta-oidc]
agents:
  - name: a
    access:
      auth_strategies: [okta-oidc]
mcp_servers:
  - name: s
    access:
      auth_strategies: [okta-oidc]
`))
	require.NoError(t, err)
	require.Equal(t, []string{"okta-oidc"}, doc.Models[0].Access.AuthStrategies)
	require.Equal(t, []string{"okta-oidc"}, doc.Agents[0].Access.AuthStrategies)
	require.Equal(t, []string{"okta-oidc"}, doc.MCPServers[0].Access.AuthStrategies)
}

// TestParseRejectsIdentityProviders checks that the removed key is an error.
// An ignored key would remove authentication from the output without an error.
func TestParseRejectsIdentityProviders(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			name: "top-level key",
			src:  "identity_providers:\n  - name: okta-oidc\n    type: openid-connect\n",
		},
		{
			name: "model access",
			src:  "models:\n  - name: m\n    access:\n      identity_providers: [okta-oidc]\n",
		},
		{
			name: "agent access",
			src:  "agents:\n  - name: a\n    access:\n      identity_providers: [okta-oidc]\n",
		},
		{
			name: "MCP server access",
			src:  "mcp_servers:\n  - name: s\n    access:\n      identity_providers: [okta-oidc]\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			require.ErrorContains(t, err, "identity_providers was renamed to auth_strategies")
		})
	}
}
