package convert

import (
	"strings"
	"testing"

	publicaigw "github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"gopkg.in/yaml.v3"
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

func TestConvertDocumentToDBLessYAML(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [chat]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {alias: m1}
providers:
  - name: p1
    type: openai
`)

	doc, err := publicaigw.Parse(src)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}

	got, _, err := ConvertDocumentToDBLessYAML(doc, Options{})
	if err != nil {
		t.Fatalf("convert typed db-less: %v", err)
	}

	want, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert yaml db-less: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("typed db-less output mismatch\nwant:\n%s\ngot:\n%s", want, got)
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

func TestConvertDBLessFlattensConsumerCredentialsAndGroups(t *testing.T) {
	src := []byte(`
consumer_groups:
  - name: devs
consumers:
  - name: alice
    type: api-key
    consumer_groups: [devs]
    credentials:
      - name: alice-key
        api_key: sk-test
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if got["_format_version"] != "3.0" {
		t.Fatalf("unexpected format version: %v", got["_format_version"])
	}
	if _, ok := got["keyauth_credentials"]; !ok {
		t.Fatalf("expected keyauth_credentials in db-less output: %s", out)
	}
	if _, ok := got["consumer_group_consumers"]; !ok {
		t.Fatalf("expected consumer_group_consumers in db-less output: %s", out)
	}
}

func TestConvertDBLessPreservesInputIDsWhenProvided(t *testing.T) {
	src := []byte(`
consumer_groups:
  - id: cg-source-id
    name: devs
consumers:
  - id: consumer-source-id
    name: alice
    type: api-key
    consumer_groups: [devs]
    credentials:
      - id: cred-source-id
        name: alice-key
        api_key: sk-test
models:
  - id: model-source-id
    type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {alias: m1}
vaults:
  - id: vault-source-id
    type: env
    name: env
providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	consumerGroups := got["consumer_groups"].([]any)
	consumerGroup := consumerGroups[0].(map[string]any)
	if consumerGroup["id"] != "cg-source-id" {
		t.Fatalf("expected consumer group id to be preserved, got %v", consumerGroup["id"])
	}

	consumers := got["consumers"].([]any)
	consumer := consumers[0].(map[string]any)
	if consumer["id"] != "consumer-source-id" {
		t.Fatalf("expected consumer id to be preserved, got %v", consumer["id"])
	}

	credentials := got["keyauth_credentials"].([]any)
	credential := credentials[0].(map[string]any)
	if credential["id"] != "cred-source-id" {
		t.Fatalf("expected credential id to be preserved, got %v", credential["id"])
	}

	members := got["consumer_group_consumers"].([]any)
	member := members[0].(map[string]any)
	if member["consumer_group"] != "cg-source-id" {
		t.Fatalf("expected consumer group membership to use preserved group id, got %v", member["consumer_group"])
	}
	if member["consumer"] != "consumer-source-id" {
		t.Fatalf("expected consumer group membership to use preserved consumer id, got %v", member["consumer"])
	}

	models := got["ai_models"].([]any)
	model := models[0].(map[string]any)
	if model["id"] != "model-source-id" {
		t.Fatalf("expected ai model id to be preserved, got %v", model["id"])
	}

	vaults := got["vaults"].([]any)
	vault := vaults[0].(map[string]any)
	if vault["id"] != "vault-source-id" {
		t.Fatalf("expected vault id to be preserved, got %v", vault["id"])
	}
}

func TestConvertDBLessKeepsExpandedRouteFields(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    target_models:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route:
        hosts: [ai.example.com]
        methods: [GET, POST]
        protocols: [http, https]
        headers:
          x-api-version: [v1]
        snis: [ai.example.com]
        sources:
          - ip: 192.168.1.0/24
            port: 8080
        destinations:
          - ip: 10.1.0.0/16
            port: 443
        strip_path: true
        preserve_host: false
        https_redirect_status_code: 426
        regex_priority: 1
        path_handling: v0
        request_buffering: true
        response_buffering: true
      model: {}
providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Routes []struct {
			Hosts     []string            `yaml:"hosts"`
			Methods   []string            `yaml:"methods"`
			Protocols []string            `yaml:"protocols"`
			Headers   map[string][]string `yaml:"headers"`
			SNIs      []string            `yaml:"snis"`
			Sources   []struct {
				IP   string `yaml:"ip"`
				Port int    `yaml:"port"`
			} `yaml:"sources"`
			Destinations []struct {
				IP   string `yaml:"ip"`
				Port int    `yaml:"port"`
			} `yaml:"destinations"`
			StripPath               *bool  `yaml:"strip_path"`
			PreserveHost            *bool  `yaml:"preserve_host"`
			HTTPSRedirectStatusCode *int   `yaml:"https_redirect_status_code"`
			RegexPriority           *int   `yaml:"regex_priority"`
			PathHandling            string `yaml:"path_handling"`
			RequestBuffering        *bool  `yaml:"request_buffering"`
			ResponseBuffering       *bool  `yaml:"response_buffering"`
		} `yaml:"routes"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(got.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d: %s", len(got.Routes), out)
	}
	route := got.Routes[0]
	if len(route.Hosts) != 1 || route.Hosts[0] != "ai.example.com" {
		t.Fatalf("unexpected hosts: %#v", route.Hosts)
	}
	if len(route.Methods) != 2 || route.Methods[0] != "GET" || route.Methods[1] != "POST" {
		t.Fatalf("unexpected methods: %#v", route.Methods)
	}
	if len(route.Protocols) != 2 || route.Protocols[0] != "http" || route.Protocols[1] != "https" {
		t.Fatalf("unexpected protocols: %#v", route.Protocols)
	}
	if got := route.Headers["x-api-version"]; len(got) != 1 || got[0] != "v1" {
		t.Fatalf("unexpected headers: %#v", route.Headers)
	}
	if len(route.SNIs) != 1 || route.SNIs[0] != "ai.example.com" {
		t.Fatalf("unexpected snis: %#v", route.SNIs)
	}
	if len(route.Sources) != 1 || route.Sources[0].IP != "192.168.1.0/24" || route.Sources[0].Port != 8080 {
		t.Fatalf("unexpected sources: %#v", route.Sources)
	}
	if len(route.Destinations) != 1 || route.Destinations[0].IP != "10.1.0.0/16" || route.Destinations[0].Port != 443 {
		t.Fatalf("unexpected destinations: %#v", route.Destinations)
	}
	if route.StripPath == nil || !*route.StripPath {
		t.Fatalf("unexpected strip_path: %#v", route.StripPath)
	}
	if route.PreserveHost == nil || *route.PreserveHost {
		t.Fatalf("unexpected preserve_host: %#v", route.PreserveHost)
	}
	if route.HTTPSRedirectStatusCode == nil || *route.HTTPSRedirectStatusCode != 426 {
		t.Fatalf("unexpected https_redirect_status_code: %#v", route.HTTPSRedirectStatusCode)
	}
	if route.RegexPriority == nil || *route.RegexPriority != 1 {
		t.Fatalf("unexpected regex_priority: %#v", route.RegexPriority)
	}
	if route.PathHandling != "v0" {
		t.Fatalf("unexpected path_handling: %q", route.PathHandling)
	}
	if route.RequestBuffering == nil || !*route.RequestBuffering {
		t.Fatalf("unexpected request_buffering: %#v", route.RequestBuffering)
	}
	if route.ResponseBuffering == nil || !*route.ResponseBuffering {
		t.Fatalf("unexpected response_buffering: %#v", route.ResponseBuffering)
	}
}

func TestConvertDBLessCredentialTTLAndTags(t *testing.T) {
	ttl := 7200
	src := []byte(`
consumers:
  - name: alice
    type: api-key
    credentials:
      - name: alice-key
        type: api-key
        api_key: sk-alice
        ttl: 7200
        labels:
          env: prod
          scope: read-only
  - name: bob
    type: api-key
    credentials:
      - name: bob-key
        type: api-key
        api_key: sk-bob
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Credentials []struct {
			Key      string   `yaml:"key"`
			Consumer string   `yaml:"consumer"`
			TTL      *int     `yaml:"ttl"`
			Tags     []string `yaml:"tags"`
		} `yaml:"keyauth_credentials"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(got.Credentials) != 2 {
		t.Fatalf("expected 2 keyauth_credentials, got %d: %s", len(got.Credentials), out)
	}

	alice := got.Credentials[0]
	if alice.Key != "sk-alice" {
		t.Fatalf("unexpected key: %q", alice.Key)
	}
	if alice.TTL == nil || *alice.TTL != ttl {
		t.Fatalf("expected TTL %d, got %v", ttl, alice.TTL)
	}
	if len(alice.Tags) != 2 || alice.Tags[0] != "env:prod" || alice.Tags[1] != "scope:read-only" {
		t.Fatalf("unexpected tags: %#v", alice.Tags)
	}

	bob := got.Credentials[1]
	if bob.Key != "sk-bob" {
		t.Fatalf("unexpected key: %q", bob.Key)
	}
	if bob.TTL != nil {
		t.Fatalf("expected no TTL for bob, got %v", bob.TTL)
	}
	if len(bob.Tags) != 0 {
		t.Fatalf("expected no tags for bob, got %#v", bob.Tags)
	}
}
