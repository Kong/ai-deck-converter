package aigw

import "testing"

const sampleDoc = `
models:
  - type: model
    display_name: My GPT model
    name: my-gpt
    capabilities: [generate]
    formats:
      - type: openai
    target_models:
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
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Models) != 1 {
		t.Fatalf("want 1 model, got %d", len(doc.Models))
	}
	m := doc.Models[0]
	if m.Name != "my-gpt" || m.Type != "model" {
		t.Errorf("model name/type = %q/%q", m.Name, m.Type)
	}
	if len(m.TargetModels) != 1 {
		t.Fatalf("want 1 target, got %d", len(m.TargetModels))
	}
	tm := m.TargetModels[0]
	if tm.Provider.Name != "openai-main" {
		t.Errorf("target provider = %q", tm.Provider.Name)
	}
	if tm.Config.Type != "openai" {
		t.Errorf("target config type = %q", tm.Config.Type)
	}
	if _, ok := tm.Config.Options["type"]; ok {
		t.Error("type should be stripped from options")
	}
	if tm.Config.Options["temperature"] != 0.7 {
		t.Errorf("temperature = %v", tm.Config.Options["temperature"])
	}
	if m.Config.Route.Name != "gpt-route" {
		t.Errorf("route name = %q", m.Config.Route.Name)
	}
	if len(doc.Providers) != 1 || doc.Providers[0].Config.Auth.Headers[0].Name != "Authorization" {
		t.Errorf("provider auth not parsed: %+v", doc.Providers)
	}
	if len(doc.Consumers) != 1 || len(doc.Consumers[0].Credentials) != 1 {
		t.Errorf("consumer/credential not parsed: %+v", doc.Consumers)
	}
	if len(doc.Vaults) != 1 || doc.Vaults[0].Type != "env" {
		t.Errorf("vault not parsed: %+v", doc.Vaults)
	}
}
