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
        alias: my-gpt
providers:
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
	require.Len(t, doc.Providers, 1, "provider not parsed")
	require.Equal(t, "Authorization", doc.Providers[0].Config.Auth.Headers[0].Name, "provider auth not parsed")
	require.Len(t, doc.Consumers, 1, "consumer not parsed")
	require.Len(t, doc.Consumers[0].Credentials, 1, "credential not parsed")
	require.Len(t, doc.Vaults, 1, "vault not parsed")
	require.Equal(t, "env", doc.Vaults[0].Type, "vault type")
}
