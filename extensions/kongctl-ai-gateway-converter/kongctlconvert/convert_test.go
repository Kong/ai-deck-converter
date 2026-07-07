package kongctlconvert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDeckToKongctlWrapsGatewayAndAdaptsFields(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
consumers:
  - username: gregs-dev
    groups:
      - premium-users
    keyauth_credentials:
      - key: secret-abc
        ttl: 3600
consumer_groups:
  - name: premium-users
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-generate
        paths:
          - /ai/chat
        plugins:
          - name: ai-proxy-advanced
            config:
              llm_format: openai
              route_type: llm/v1/chat
              targets:
                - route_type: llm/v1/chat
                  model:
                    provider: openai
                    name: gpt-4o
                  auth:
                    header_name: Authorization
                    header_value: "{vault://env/openai-key}"
ai-models:
  - name: guarded-gpt
    alias: "@openai/guarded-gpt"
`)

	out, warnings, err := Convert(src, Options{
		From:               FormatDeck,
		To:                 FormatKongctl,
		GatewayName:        "support-ai",
		GatewayDisplayName: "Support AI",
	})
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	require.Contains(t, strings.Join(warnings, "\n"), "consumer_groups is not supported")
	require.Contains(t, strings.Join(warnings, "\n"), "api_key is write-only")

	doc := decodeYAML(t, out)
	gateways := mapsFromAny(doc[topLevelGateways])
	require.Len(t, gateways, 1)
	gateway := gateways[0]
	require.Equal(t, "support-ai", gateway["ref"])
	require.Equal(t, "support-ai", gateway["name"])
	require.Equal(t, "Support AI", gateway["display_name"])

	models := mapsFromAny(gateway["models"])
	require.NotEmpty(t, models)
	var translatedModel map[string]any
	for _, model := range models {
		if _, ok := model["target_models"]; ok {
			translatedModel = model
			break
		}
	}
	require.NotNil(t, translatedModel)
	require.NotContains(t, translatedModel, "targets")
	require.NotEmpty(t, translatedModel["display_name"])

	consumers := mapsFromAny(gateway["consumers"])
	require.Len(t, consumers, 1)
	require.NotContains(t, consumers[0], "consumer_groups")
	credentials := mapsFromAny(consumers[0]["credentials"])
	require.Len(t, credentials, 1)
	require.Equal(t, "gregs-dev-credential", credentials[0]["name"])
	require.NotContains(t, credentials[0], "api_key")
}

func TestDeckToKongctlStrictFailsOnAdapterWarnings(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
consumers:
  - username: gregs-dev
    keyauth_credentials:
      - key: secret-abc
`)

	_, warnings, err := Convert(src, Options{
		From:        FormatDeck,
		To:          FormatKongctl,
		GatewayName: "support-ai",
		Strict:      true,
	})
	require.Error(t, err)
	require.Contains(t, strings.Join(warnings, "\n"), "api_key is write-only")
}

func TestKongctlToDeckSelectsGatewayAndRootChildren(t *testing.T) {
	src := []byte(`
ai_gateways:
  - ref: support-ref
    name: support-ai
    display_name: Support AI
    providers:
      - ref: openai-provider
        name: openai-provider
        type: openai
        display_name: OpenAI Provider
        config:
          auth:
            type: basic
            headers:
              - name: Authorization
                value: "{vault://env/openai-key}"
    models:
      - ref: support-model
        type: model
        name: support-model
        display_name: Support Model
        capabilities:
          - generate
        formats:
          - type: openai
        target_models:
          - name: gpt-4o
            provider: openai-provider
            config:
              type: openai
        config:
          route:
            paths:
              - /ai
          model:
            alias: "@openai/support"
  - ref: other-ref
    name: other-ai
    display_name: Other AI
ai_gateway_mcp_servers:
  - ref: tools
    ai_gateway: support-ref
    type: passthrough-listener
    name: tools
    display_name: Tools
    config:
      url: https://tools.example.com/mcp
      route:
        paths:
          - /tools
    tools:
      - name: lookup
        description: Lookup
ai_gateway_consumer_credentials:
  - ref: support-key
    ai_gateway_consumer: support-consumer
    name: support-key
    type: api-key
    display_name: Support Key
    ttl: 0
ai_gateway_consumers:
  - ref: support-consumer
    ai_gateway: support-ref
    name: support-consumer
    type: api-key
    display_name: Support Consumer
`)

	out, warnings, err := Convert(src, Options{
		From:        FormatKongctl,
		To:          FormatDeck,
		GatewayName: "support-ai",
	})
	require.NoError(t, err)
	require.Empty(t, warnings)

	doc := decodeYAML(t, out)
	require.Equal(t, "3.0", doc["_format_version"])
	require.NotEmpty(t, mapsFromAny(doc["services"]))
	require.NotEmpty(t, mapsFromAny(doc["consumers"]))

	rendered := string(out)
	require.Contains(t, rendered, "name: ai-proxy-advanced")
	require.Contains(t, rendered, "name: ai-mcp-proxy")
	require.Contains(t, rendered, "keyauth_credentials:")
	require.NotContains(t, rendered, "target_models:")
	require.NotContains(t, rendered, "ai_gateway:")
}

func TestKongctlToDeckRequiresUniqueGatewayName(t *testing.T) {
	src := []byte(`
ai_gateways:
  - ref: one
    name: support-ai
    display_name: One
  - ref: two
    name: support-ai
    display_name: Two
`)

	_, _, err := Convert(src, Options{
		From:        FormatKongctl,
		To:          FormatDeck,
		GatewayName: "support-ai",
	})
	require.ErrorContains(t, err, `matched 2 gateways`)
}

func decodeYAML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc
}
