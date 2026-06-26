package convert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	publicaigw "github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/internal/aigw"
)

func TestConvertWarnsUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
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
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "unknown provider",
		"expected unknown-provider warning")
}

// A Kong acl plugin permits exactly one of config.allow / config.deny
// (only_one_of in its schema). An AI Gateway acl that sets both is not
// representable as a single valid acl plugin, so the converter must reject it
// rather than emit structurally invalid config (it does no schema validation).
func TestConvertRejectsACLWithAllowAndDeny(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    acls:
      allow: [premium]
      deny: [banned]
    config:
      route: {paths: [/ai]}
      model: {alias: m1}
providers:
  - name: p1
    type: openai
`)
	// Invalid in every output mode, so it is rejected regardless of -strict.
	for _, mode := range []string{"", "db-less"} {
		_, _, err := Convert(src, Options{OutputMode: mode})
		require.Error(t, err, "acl with both allow and deny must be rejected (mode %q)", mode)
		require.Contains(t, err.Error(), "allow")
		require.Contains(t, err.Error(), "deny")
	}
}

func TestConvertDocumentToDBLessYAML(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [chat]
    formats: [{type: openai}]
    targets:
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
	require.NoError(t, err, "parse source")

	got, _, err := ConvertDocumentToDBLessYAML(doc, Options{})
	require.NoError(t, err, "convert typed db-less")

	want, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert yaml db-less")

	require.Equal(t, string(want), string(got), "typed db-less output mismatch")
}

func TestConvertMapsConfiguredModelAlias(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {alias: "@openai/custom-m1"}
providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	aiModels, ok := got["ai-models"].([]any)
	require.True(t, ok, "expected ai-models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai-models entry")
	require.Equal(t, "m1", aiModel["name"])
	require.Equal(t, "@openai/custom-m1", aiModel["alias"], "ai-models alias should match source model.alias")

	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")

	var proxy map[string]any
	for _, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		require.True(t, ok, "expected plugin entry")
		if plugin["name"] == "ai-proxy-advanced" {
			proxy = plugin
			break
		}
	}
	require.NotNil(t, proxy, "expected ai-proxy-advanced plugin")

	cfg, ok := proxy["config"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced config")
	targets, ok := cfg["targets"].([]any)
	require.True(t, ok, "expected ai-proxy-advanced targets")
	require.Len(t, targets, 1)
	target, ok := targets[0].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced target")
	model, ok := target["model"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced model")
	require.Equal(t, "gpt-4o", model["name"])
	require.Equal(t, "@openai/custom-m1", model["model_alias"], "target model_alias should match source model.alias")
}

func TestConvertStrictFailsUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
`)
	_, _, err := Convert(src, Options{Strict: true})
	require.Error(t, err, "expected strict mode to fail on unknown provider")
}

func TestConvertWarnsUnknownPolicy(t *testing.T) {
	src := []byte(`
consumers:
  - name: c1
    type: api-key
    policies: [missing-policy]
`)
	_, warnings, err := Convert(src, Options{})
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "unknown policy",
		"expected unknown-policy warning")
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
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "no description",
		"expected missing-description warning")
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
	_, _, err := Convert(src, Options{Strict: true})
	require.Error(t, err, "expected strict mode to fail on MCP tool missing description")
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
	require.True(t, ok, "expected logging block, got %v", a2aPlugin(a).Config["logging"])
	require.NotContains(t, logging, "log_audits", "ai-a2a-proxy must not emit log_audits, got %v", logging)
	require.Equal(t, true, logging["log_statistics"], "expected log_statistics true")
}

func TestConvertDisabledAgentDisablesService(t *testing.T) {
	src := []byte(`
agents:
  - type: a2a
    name: off-agent
    enabled: false
    config:
      url: https://a.internal/a2a
      route: {paths: [/agents/off]}
  - type: http
    name: on-agent
    enabled: true
    config:
      url: https://b.internal/api
      route: {paths: [/agents/on]}
`)
	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert db-less")

	var got struct {
		Services []struct {
			Name    string `yaml:"name"`
			Enabled *bool  `yaml:"enabled"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	byName := map[string]*bool{}
	for _, s := range got.Services {
		byName[s.Name] = s.Enabled
	}
	require.Contains(t, byName, "off-agent")
	require.NotNil(t, byName["off-agent"], "disabled agent must emit enabled")
	require.False(t, *byName["off-agent"], "disabled agent service must be enabled=false")
	require.Nil(t, byName["on-agent"], "enabled agent should not emit the flag")
}

func TestConvertDisabledMCPServerDisablesService(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: off-mcp
    enabled: false
    config:
      route: {paths: [/mcp/off]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
  - type: passthrough-listener
    name: on-mcp
    enabled: true
    upstream_url: https://b.internal/mcp
    config:
      route: {paths: [/mcp/on]}
`)
	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert db-less")

	var got struct {
		Services []struct {
			Name    string `yaml:"name"`
			Enabled *bool  `yaml:"enabled"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	byName := map[string]*bool{}
	for _, s := range got.Services {
		byName[s.Name] = s.Enabled
	}
	require.Contains(t, byName, "off-mcp")
	require.NotNil(t, byName["off-mcp"], "disabled MCP server must emit enabled")
	require.False(t, *byName["off-mcp"], "disabled MCP server service must be enabled=false")
	require.Nil(t, byName["on-mcp"], "enabled MCP server should not emit the flag")
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
	require.NoError(t, err, "convert db-less")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	require.Equal(t, "3.0", got["_format_version"], "unexpected format version")
	require.Contains(t, got, "keyauth_credentials", "expected keyauth_credentials in db-less output: %s", out)
	require.Contains(t, got, "consumer_group_consumers", "expected consumer_group_consumers in db-less output: %s", out)
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
    targets:
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
    targets:
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
	require.NoError(t, err, "convert db-less")

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
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Routes, 1, "expected 1 route: %s", out)
	route := got.Routes[0]
	require.Equal(t, []string{"ai.example.com"}, route.Hosts, "unexpected hosts")
	require.Equal(t, []string{"GET", "POST"}, route.Methods, "unexpected methods")
	require.Equal(t, []string{"http", "https"}, route.Protocols, "unexpected protocols")
	require.Equal(t, []string{"v1"}, route.Headers["x-api-version"], "unexpected headers")
	require.Equal(t, []string{"ai.example.com"}, route.SNIs, "unexpected snis")
	require.Len(t, route.Sources, 1, "unexpected sources")
	require.Equal(t, "192.168.1.0/24", route.Sources[0].IP, "unexpected source ip")
	require.Equal(t, 8080, route.Sources[0].Port, "unexpected source port")
	require.Len(t, route.Destinations, 1, "unexpected destinations")
	require.Equal(t, "10.1.0.0/16", route.Destinations[0].IP, "unexpected destination ip")
	require.Equal(t, 443, route.Destinations[0].Port, "unexpected destination port")
	require.NotNil(t, route.StripPath, "unexpected strip_path")
	require.True(t, *route.StripPath, "unexpected strip_path")
	require.NotNil(t, route.PreserveHost, "unexpected preserve_host")
	require.False(t, *route.PreserveHost, "unexpected preserve_host")
	require.NotNil(t, route.HTTPSRedirectStatusCode, "unexpected https_redirect_status_code")
	require.Equal(t, 426, *route.HTTPSRedirectStatusCode, "unexpected https_redirect_status_code")
	require.NotNil(t, route.RegexPriority, "unexpected regex_priority")
	require.Equal(t, 1, *route.RegexPriority, "unexpected regex_priority")
	require.Equal(t, "v0", route.PathHandling, "unexpected path_handling")
	require.NotNil(t, route.RequestBuffering, "unexpected request_buffering")
	require.True(t, *route.RequestBuffering, "unexpected request_buffering")
	require.NotNil(t, route.ResponseBuffering, "unexpected response_buffering")
	require.True(t, *route.ResponseBuffering, "unexpected response_buffering")
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
