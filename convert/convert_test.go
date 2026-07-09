package convert

import (
	"strings"
	"testing"

	publicaigw "github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
    access:
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
    access:
      acls:
        allow: [premium]
        deny: [banned]
    config:
      route: {paths: [/oidc-repro/model-a]}
      model: {alias: m1}
model_providers:
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

// A model's policies list must not reference an authentication policy
// (key-auth/openid-connect) directly: those require anonymous fallback and a
// companion anonymous consumer, which only the identity_providers mechanism
// provides. Referencing one via policies is rejected rather than silently
// emitting an auth plugin with no anonymous fallback.
func TestConvertRejectsAuthPolicyOnModel(t *testing.T) {
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
    policies: [my-key-auth]
    config:
      route: {paths: [/oidc-repro/model-b]}
      model: {alias: m1}
model_providers:
  - name: p1
    type: openai
policies:
  - type: key-auth
    name: my-key-auth
    config: {key_names: [apikey]}
`)
	_, _, err := Convert(src, Options{})
	require.Error(t, err, "model policies referencing key-auth/openid-connect must be rejected")
	require.Contains(t, err.Error(), "identity_providers")
}

func TestConvertScopesIdentityProvidersWithoutLeakingAcrossSharedRoutes(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: public-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-public
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/oidc-repro/model-a]}
      model: {alias: public}
  - type: model
    name: protected-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-protected
        provider: p1
        config: {type: openai}
    access:
      identity_providers: [oidc]
    config:
      route: {paths: [/oidc-repro/model-b]}
      model: {alias: protected}
model_providers:
  - name: p1
    type: openai
identity_providers:
  - name: oidc
    type: openid-connect
    config: {issuer: https://id.example.test}
`)

	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var doc struct {
		Plugins []struct {
			Name  string `yaml:"name"`
			Route string `yaml:"route"`
			Model string `yaml:"model"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var oidcRoute, publicRoute, protectedRoute string
	for _, plugin := range doc.Plugins {
		if plugin.Name == "openid-connect" {
			require.Empty(t, plugin.Model, "OIDC must be enforced at route scope")
			oidcRoute = plugin.Route
		}
		if plugin.Name == "ai-proxy-advanced" {
			switch plugin.Model {
			case "public-model":
				publicRoute = plugin.Route
			case "protected-model":
				protectedRoute = plugin.Route
			}
		}
	}
	require.NotEmpty(t, oidcRoute)
	require.Equal(t, protectedRoute, oidcRoute, "OIDC guards only the protected model's route")
	require.NotEqual(t, publicRoute, oidcRoute, "the public model must not share the OIDC route")
}

func TestConvertSharesIdentityProviderForUniformlyGuardedSharedRoute(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: model-a
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-a
        provider: p1
        config: {type: openai}
    access: {identity_providers: [oidc]}
    config: {route: {paths: [/ai]}, model: {alias: a}}
  - type: model
    name: model-b
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-b
        provider: p1
        config: {type: openai}
    access: {identity_providers: [oidc]}
    config: {route: {paths: [/ai]}, model: {alias: b}}
model_providers:
  - name: p1
    type: openai
identity_providers:
  - name: oidc
    type: openid-connect
`)

	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var doc struct {
		Plugins []struct {
			Name  string `yaml:"name"`
			Route string `yaml:"route"`
			Model string `yaml:"model"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var count int
	for _, plugin := range doc.Plugins {
		if plugin.Name == "openid-connect" {
			count++
			require.Equal(t, "openai-chat", plugin.Route)
			require.Empty(t, plugin.Model, "uniformly guarded models share the route plugin")
		}
	}
	require.Equal(t, 1, count)
}

// The Kong acl plugin checks a legacy per-consumer kong.db.acls entity by
// default, not Enterprise consumer_groups membership, unless
// config.include_consumer_groups is set. AI Gateway's only group-membership
// construct is consumer_groups (the converter never creates legacy acls
// rows), so an emitted acl plugin must always set include_consumer_groups —
// otherwise its allow/deny list can never match anything and the plugin can
// never allow a request.
func TestConvertACLIncludesConsumerGroups(t *testing.T) {
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
    access:
      acls:
        allow: [premium-users]
    config:
      route: {paths: [/ai]}
      model: {alias: m1}
model_providers:
  - name: p1
    type: openai
`)
	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var doc struct {
		Plugins []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var acl *struct {
		Name   string
		Config map[string]any
	}
	for i := range doc.Plugins {
		if doc.Plugins[i].Name == "acl" {
			acl = &struct {
				Name   string
				Config map[string]any
			}{doc.Plugins[i].Name, doc.Plugins[i].Config}
		}
	}
	require.NotNil(t, acl, "expected an acl plugin in the output")
	require.Equal(t, true, acl.Config["include_consumer_groups"],
		"acl plugin must set include_consumer_groups so consumer_groups-based allow/deny actually works")
}

// ai-mcp-proxy has the identical include_consumer_groups gap as the Kong acl
// plugin (see TestConvertACLIncludesConsumerGroups above): its own ACL-subject
// extraction only recognizes consumer_groups membership when the flag is set,
// defaulting to false. mcpPlugin() must set it by default — including for an
// MCP server with no ACL configured at all (open-mcp below), which proves the
// fix doesn't only trigger when default_acl/tools[].acl is present — except
// when acl_attribute_type is oauth_access_token (oauth-mcp below), where
// ai-mcp-proxy's own schema hard-rejects include_consumer_groups being set.
func TestConvertMCPIncludesConsumerGroups(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: guarded-mcp
    access:
      default_tool_acls:
        allow: [premium-users]
    config:
      route: {paths: [/mcp/guarded]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
  - type: conversion-listener
    name: open-mcp
    config:
      route: {paths: [/mcp/open]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
  - type: conversion-listener
    name: oauth-mcp
    config:
      route: {paths: [/mcp/oauth]}
      access:
        acl_attribute_type: oauth_access_token
        access_token_claim_field: .user.email
        default_tool_acls:
          allow: [premium-users]
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
`)
	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert db-less")

	var doc struct {
		Plugins []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var mcpPlugins []map[string]any
	for i := range doc.Plugins {
		if doc.Plugins[i].Name == "ai-mcp-proxy" {
			mcpPlugins = append(mcpPlugins, doc.Plugins[i].Config)
		}
	}
	require.Len(t, mcpPlugins, 3, "expected an ai-mcp-proxy plugin per MCP server")
	for _, cfg := range mcpPlugins[:2] {
		require.Equal(t, true, cfg["include_consumer_groups"],
			"ai-mcp-proxy plugin must set include_consumer_groups so consumer_groups-based allow/deny actually works")
	}
	// oauth_access_token is the exception: ai-mcp-proxy's schema hard-rejects
	// include_consumer_groups being set in that mode (and subjects.lua ignores it anyway),
	// so it must be left unset, not forced to true.
	_, isSet := mcpPlugins[2]["include_consumer_groups"]
	require.False(t, isSet,
		"ai-mcp-proxy plugin must NOT set include_consumer_groups when "+
			"acl_attribute_type is oauth_access_token: Kong's schema rejects that")
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
model_providers:
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

func TestConvertDBLessPreservesProvidedPolicyIDAndGeneratesMissingOnes(t *testing.T) {
	src := []byte(`
policies:
  - id: provided-policy-id
    type: rate-limiting
    name: org-wide-limit
    global: true
    config:
      minute: 1000
      policy: local
  - type: key-auth
    name: require-key
    global: true
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert db-less")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")
	require.Len(t, plugins, 2)

	pluginsByName := make(map[string]map[string]any, len(plugins))
	for _, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		require.True(t, ok, "expected plugin entry")
		pluginsByName[plugin["name"].(string)] = plugin
	}

	require.Equal(t, "provided-policy-id", pluginsByName["rate-limiting"]["id"])

	keyAuthID, ok := pluginsByName["key-auth"]["id"].(string)
	require.True(t, ok, "expected generated key-auth plugin id")
	require.NotEmpty(t, keyAuthID)
	require.NotEqual(t, "provided-policy-id", keyAuthID)
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
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai_models entry")
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

// For type "model", the target model_alias defaults to the model name when
// unset, matching the ai-models alias. Koko only ever routes to this
// model-scoped plugin via an ai-model-selector match on that same alias, so
// the target only ever sees a request body model value equal to it; without
// this default, ai-proxy-advanced's own "cannot use own model" check would
// reject that value since no model_alias would be present to match against.
func TestConvertSynthesizesTargetModelAliasWhenUnsetForModelScoped(t *testing.T) {
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
      model: {}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai_models entry")
	require.Equal(t, "m1", aiModel["name"])
	require.Equal(t, "m1", aiModel["alias"],
		"ai-models alias should fall back to the model name when source model.alias is unset")

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
	require.Equal(t, "m1", model["model_alias"], "type \"model\" target model_alias should default to the model name when source model.alias is unset")
}

func TestConvertDBLessSynthesizesAIModelAliasWhenUnset(t *testing.T) {
	src := []byte(`
models:
  - type: api
    name: files-api
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {name_header: false}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert db-less")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal db-less output")

	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai_models entry")
	require.Equal(t, "files-api", aiModel["name"])
	require.Equal(t, "files-api", aiModel["alias"],
		"db-less ai_models alias should fall back to the model name when source model.alias is unset")

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
	require.Equal(t, "files", model["name"])
	_, hasModelAlias := model["model_alias"]
	require.False(t, hasModelAlias, "db-less target model_alias should still be omitted when source model.alias is unset")
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
    access:
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

func TestConvertDisabledModelDisablesProxyPlugins(t *testing.T) {
	src := []byte(`
model_providers:
  - name: openai
    type: openai
models:
  - type: model
    name: disabled
    enabled: false
    capabilities: [agentic, generate, image]
    formats: [{type: openai}]
    targets: [{name: gpt-5, provider: openai, config: {type: openai}}]
    config:
      route: {paths: [/gpt-chat]}
      model: {alias: gpt-chat}
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	require.NoError(t, err, "convert db-less")

	var got struct {
		Plugins []struct {
			Name    string `yaml:"name"`
			Enabled *bool  `yaml:"enabled"`
			Model   *struct {
				Name string `yaml:"name"`
			} `yaml:"model"`
		} `yaml:"plugins"`
		AIModels []struct {
			Name string `yaml:"name"`
		} `yaml:"ai_models"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	require.Len(t, got.AIModels, 1)
	require.Equal(t, "disabled", got.AIModels[0].Name)

	proxyPlugins := 0
	for _, plugin := range got.Plugins {
		if plugin.Name == "ai-proxy-advanced" && plugin.Model != nil {
			require.NotNil(t, plugin.Enabled)
			require.False(t, *plugin.Enabled)
			proxyPlugins++
		}
	}
	require.Equal(t, 3, proxyPlugins, "one disabled proxy plugin per capability")
}

func TestConvertModelsWithDifferentRoutesDoNotShareEndpointRoute(t *testing.T) {
	src := []byte(`
model_providers:
  - name: xai
    type: xai
  - name: openai
    type: openai
models:
  - type: model
    name: xai-chat
    formats: [{type: openai}]
    capabilities: [generate]
    targets: [{name: grok-4, provider: xai, config: {type: xai}}]
    config:
      route: {paths: [/xai-chat]}
      model: {alias: xai-chat}
  - type: model
    name: openai-chat
    formats: [{type: openai}]
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai, config: {type: openai}}]
    config:
      route: {paths: [/openai-chat]}
      model: {alias: openai-chat}
`)

	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var got struct {
		Services []struct {
			Routes []struct {
				Name  string   `yaml:"name"`
				Paths []string `yaml:"paths"`
			} `yaml:"routes"`
		} `yaml:"services"`
		Plugins []struct {
			Name  string `yaml:"name"`
			Route string `yaml:"route"`
			Model string `yaml:"model"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Services, 1)
	require.Len(t, got.Services[0].Routes, 2)

	routeByPath := map[string]string{}
	for _, route := range got.Services[0].Routes {
		require.Len(t, route.Paths, 1)
		routeByPath[route.Paths[0]] = route.Name
	}
	require.Equal(t, "openai-chat", routeByPath["/xai-chat/chat/completions"])
	require.Equal(t, "openai-chat-2", routeByPath["/openai-chat/chat/completions"])

	pluginRouteByModel := map[string]string{}
	for _, plugin := range got.Plugins {
		if plugin.Name == "ai-proxy-advanced" {
			pluginRouteByModel[plugin.Model] = plugin.Route
		}
	}
	require.Equal(t, routeByPath["/xai-chat/chat/completions"], pluginRouteByModel["xai-chat"])
	require.Equal(t, routeByPath["/openai-chat/chat/completions"], pluginRouteByModel["openai-chat"])
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
model_providers:
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
    access:
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
model_providers:
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

// A model's `labels` map, like every other entity's, must convert into the
// low-level `tags` field on its ai-models entry via labelsToTags.
func TestConvertDBLessModelLabelsToTags(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: prod-model
    capabilities: [generate]
    formats: [{type: openai}]
    labels:
      env: prod
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/prod]}
      model: {alias: prod-model}
  - type: model
    name: plain-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/plain]}
      model: {alias: plain-model}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		AIModels []struct {
			Name string   `yaml:"name"`
			Tags []string `yaml:"tags"`
		} `yaml:"ai_models"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(got.AIModels) != 2 {
		t.Fatalf("expected 2 ai_models, got %d: %s", len(got.AIModels), out)
	}

	prod := got.AIModels[0]
	if prod.Name != "prod-model" {
		t.Fatalf("unexpected name: %q", prod.Name)
	}
	if len(prod.Tags) != 1 || prod.Tags[0] != "env:prod" {
		t.Fatalf("unexpected tags: %#v", prod.Tags)
	}

	plain := got.AIModels[1]
	if plain.Name != "plain-model" {
		t.Fatalf("unexpected name: %q", plain.Name)
	}
	if len(plain.Tags) != 0 {
		t.Fatalf("expected no tags for plain-model, got %#v", plain.Tags)
	}
}

func TestConvertDBLessMCPLabelsBecomePluginTags(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-only
    name: team-a
    labels:
      aigw610: tools
    config:
      route: {paths: [/mcp/team-a]}
      url: https://team-a.internal.example.com/mcp
      tools:
        - name: team-a-get-report
          description: Get a report
          method: GET
          path: /report
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      server:
        tag: aigw610:tools
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Services []struct {
			Name string   `yaml:"name"`
			Tags []string `yaml:"tags"`
		} `yaml:"services"`
		Plugins []struct {
			Name   string         `yaml:"name"`
			Tags   []string       `yaml:"tags"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	var teamAServiceTags []string
	for _, service := range got.Services {
		if service.Name == "team-a" {
			teamAServiceTags = service.Tags
			break
		}
	}
	if len(teamAServiceTags) != 1 || teamAServiceTags[0] != "aigw610:tools" {
		t.Fatalf("unexpected team-a service tags: %#v", teamAServiceTags)
	}

	var conversionOnlyTags []string
	var listenerTag string
	for _, plugin := range got.Plugins {
		if plugin.Name != "ai-mcp-proxy" {
			continue
		}
		if plugin.Config["mode"] == "conversion-only" {
			conversionOnlyTags = plugin.Tags
		}
		if plugin.Config["mode"] == "listener" {
			server, _ := plugin.Config["server"].(map[string]any)
			listenerTag, _ = server["tag"].(string)
		}
	}

	if len(conversionOnlyTags) != 1 || conversionOnlyTags[0] != "aigw610:tools" {
		t.Fatalf("unexpected conversion-only plugin tags: %#v", conversionOnlyTags)
	}
	if listenerTag != "aigw610:tools" {
		t.Fatalf("unexpected listener tag: %q", listenerTag)
	}
}

func TestConvertScopedPoliciesDoNotReusePolicyID(t *testing.T) {
	src := []byte(`
policies:
  - id: policy-123
    name: sanitizer
    type: ai-sanitizer
    config:
      host: localhost
models:
  - type: model
    name: model-one
    capabilities: [generate]
    formats: [{type: openai}]
    policies: [sanitizer]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/model-one]}
      model: {alias: model-one}
  - type: api
    name: api-one
    capabilities: [files]
    formats: [{type: openai}]
    policies: [sanitizer]
    targets:
      - name: files-api
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/api-one]}
      model: {name_header: false}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := Convert(src, Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Plugins []struct {
			ID    string `yaml:"id"`
			Name  string `yaml:"name"`
			Route struct {
				ID string `yaml:"id"`
			} `yaml:"route"`
			Model struct {
				ID string `yaml:"id"`
			} `yaml:"model"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	var sanitizerCount int
	seenIDs := map[string]bool{}
	for _, plugin := range got.Plugins {
		if plugin.Name != "ai-sanitizer" {
			continue
		}
		sanitizerCount++
		if plugin.ID == "" {
			t.Fatalf("scoped ai-sanitizer plugin missing ID: %#v", plugin)
		}
		if plugin.ID == "policy-123" {
			t.Fatalf("scoped ai-sanitizer plugin unexpectedly preserved source policy ID %q", plugin.ID)
		}
		if seenIDs[plugin.ID] {
			t.Fatalf("scoped ai-sanitizer plugins unexpectedly reused generated ID %q", plugin.ID)
		}
		seenIDs[plugin.ID] = true
		if plugin.Route.ID == "" {
			t.Fatalf("scoped ai-sanitizer plugin missing route ref: %#v", plugin)
		}
	}

	if sanitizerCount != 2 {
		t.Fatalf("expected 2 scoped ai-sanitizer plugins, got %d: %s", sanitizerCount, out)
	}
}
