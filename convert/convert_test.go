package convert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
)

func TestConvertWarnsUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
`)
	_, warnings, err := Convert(src, Options{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsSubstr(warnings, "unknown provider") {
		t.Errorf("expected unknown-provider warning, got %v", warnings)
	}
}

func TestConvertStrictFailsUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
`)
	if _, _, err := Convert(src, Options{Strict: true}); err == nil {
		t.Error("expected strict mode to fail on unknown provider")
	}
}

func TestConvertWarnsUnknownPolicy(t *testing.T) {
	src := []byte(`
consumers:
  - name: c1
    type: api-key
    policies: [missing-policy]
`)
	_, warnings, err := Convert(src, Options{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsSubstr(warnings, "unknown policy") {
		t.Errorf("expected unknown-policy warning, got %v", warnings)
	}
}

func TestConvertWarnsMCPToolMissingDescription(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: m1
    config:
      route: {paths: [/mcp]}
    tools:
      - name: noDescTool
`)
	_, warnings, err := Convert(src, Options{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsSubstr(warnings, "no description") {
		t.Errorf("expected missing-description warning, got %v", warnings)
	}
}

func TestConvertStrictFailsMCPToolMissingDescription(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: m1
    config:
      route: {paths: [/mcp]}
    tools:
      - name: noDescTool
`)
	if _, _, err := Convert(src, Options{Strict: true}); err == nil {
		t.Error("expected strict mode to fail on MCP tool missing description")
	}
}

func TestA2APluginDropsLogAudits(t *testing.T) {
	a := &aigw.Agent{
		Type: "a2a",
		Name: "agent1",
		Config: aigw.AgentConfig{
			Logging: &aigw.Logging{
				Statistics: boolPtr(true),
				Audits:     boolPtr(true), // ai-mcp-proxy field; invalid for ai-a2a-proxy
			},
		},
	}
	logging, ok := a2aPlugin(a).Config["logging"].(map[string]any)
	if !ok {
		t.Fatalf("expected logging block, got %v", a2aPlugin(a).Config["logging"])
	}
	if _, present := logging["log_audits"]; present {
		t.Errorf("ai-a2a-proxy must not emit log_audits, got %v", logging)
	}
	if logging["log_statistics"] != true {
		t.Errorf("expected log_statistics true, got %v", logging["log_statistics"])
	}
}

func containsSubstr(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
