package convert

import (
	"fmt"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

// mcpListenerWithAccess is a key-auth protected listener over two
// conversion-only sources, plus an unexposed conversion-only server.
const mcpListenerWithAccess = `
auth_strategies:
  - name: mcp-key-auth
    type: key-auth
    config:
      key_names: [apikey]
mcp_servers:
  - type: conversion-only
    name: toolset-a
    config:
      route: {paths: [/mcp/a]}
    tools:
      - {name: report-a, description: Get a report, method: GET, path: /report}
  - type: conversion-only
    name: toolset-b
    config:
      route: {paths: [/mcp/b]}
    tools:
      - {name: report-b, description: Get a report, method: GET, path: /report}
  - type: conversion-only
    name: unexposed
    config:
      route: {paths: [/mcp/unexposed]}
    tools:
      - {name: report-c, description: Get a report, method: GET, path: /report}
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      sources: [toolset-a, toolset-b]
    access:
      auth_strategies: [mcp-key-auth]
`

func convertMCP(t *testing.T, src string) (*kong.Document, []string) {
	t.Helper()
	doc, err := aigw.Parse([]byte(src))
	require.NoError(t, err)
	out, warnings, err := ConvertDocument(doc, Options{})
	require.NoError(t, err)
	return out, warnings
}

func routePlugins(t *testing.T, doc *kong.Document, service string) []kong.Plugin {
	t.Helper()
	for i := range doc.Services {
		if doc.Services[i].Name != service {
			continue
		}
		require.Len(t, doc.Services[i].Routes, 1)
		return doc.Services[i].Routes[0].Plugins
	}
	t.Fatalf("service %q not found", service)
	return nil
}

func serviceNames(doc *kong.Document) []string {
	names := make([]string, 0, len(doc.Services))
	for i := range doc.Services {
		names = append(names, doc.Services[i].Name)
	}
	return names
}

func pluginNames(t *testing.T, doc *kong.Document, service string) []string {
	t.Helper()
	var names []string
	for _, p := range routePlugins(t, doc, service) {
		names = append(names, p.Name)
	}
	return names
}

func TestConversionOnlySourcesAreGated(t *testing.T) {
	out, warnings := convertMCP(t, mcpListenerWithAccess)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `MCP server "unexposed" is conversion-only but no listener names it`)

	// The exposed sources are closed to clients by the gate rather than
	// carrying a copy of the listener's auth; the listener keeps its own.
	require.Equal(t, []string{"ai-mcp-proxy", "pre-function"}, pluginNames(t, out, "toolset-a"))
	require.Equal(t, []string{"ai-mcp-proxy", "pre-function"}, pluginNames(t, out, "toolset-b"))
	require.Equal(t, []string{"ai-mcp-proxy", "key-auth"}, pluginNames(t, out, "aggregate"))
	// "unexposed" is named by no listener, so it is dropped rather than
	// published as an endpoint nothing reaches.
	require.NotContains(t, serviceNames(out), "unexposed")

	gateA := routePlugins(t, out, "toolset-a")[1]
	require.Equal(t, []string{aimap.MCPToolsetGateTag}, gateA.Tags)
	require.Equal(t, map[string]any{"access": []any{aimap.MCPToolsetGateAccess}}, gateA.Config)

	// Each route's gate has its own config, so the two cannot alias.
	gateB := routePlugins(t, out, "toolset-b")[1]
	gateA.Config["access"] = []any{"changed"}
	require.Equal(t, []any{aimap.MCPToolsetGateAccess}, gateB.Config["access"])
}

func TestListenerWithoutAccessStillGatesSources(t *testing.T) {
	// No access on the listener: the source is gated all the same, since its
	// tools are only ever meant to be reached through the listener.
	out, warnings := convertMCP(t, `
mcp_servers:
  - type: conversion-only
    name: toolset-a
    config:
      route: {paths: [/mcp/a]}
    tools:
      - {name: report-a, description: Get a report, method: GET, path: /report}
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      sources: [toolset-a]
`)
	require.Empty(t, warnings)
	require.Equal(t, []string{"ai-mcp-proxy", "pre-function"}, pluginNames(t, out, "toolset-a"))
	require.Equal(t, []string{"ai-mcp-proxy"}, pluginNames(t, out, "aggregate"))
}

// mcpConflictingListeners exposes one source from two listeners whose key-auth
// strategies disagree.
const mcpConflictingListeners = `
auth_strategies:
  - name: key-a
    type: key-auth
    config:
      key_names: [apikey]
  - name: key-b
    type: key-auth
    config:
      key_names: [other-key]
mcp_servers:
  - type: conversion-only
    name: shared
    config:
      route: {paths: [/mcp/shared]}
    tools:
      - {name: report, description: Get a report, method: GET, path: /report}
  - type: listener
    name: first
    config:
      route: {paths: [/mcp/first]}
      sources: [shared]
    access:
      auth_strategies: [key-a]
  - type: listener
    name: second
    config:
      route: {paths: [/mcp/second]}
      sources: [shared]
    access:
      auth_strategies: [key-b]
`

// mcpPreFunctionPolicy gives a conversion-only source a policy of the gate's
// own plugin type, route-scoped or global.
const mcpPreFunctionPolicy = `
policies:
  - name: my-pre-function
    type: pre-function
    global: %t
    config:
      access: ['kong.log.notice("hello")']
mcp_servers:
  - type: conversion-only
    name: toolset-a
    config:
      route: {paths: [/mcp/a]}
    policies: [my-pre-function]
    tools:
      - {name: report-a, description: Get a report, method: GET, path: /report}
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      sources: [toolset-a]
`

func TestConversionOnlySourceRejectsPreFunctionPolicy(t *testing.T) {
	// Kong allows one plugin per name on a route: emitting the policy next to
	// the gate would produce a config Kong refuses to load. It is a hard error
	// even outside -strict, since dropping either plugin changes behavior.
	_, _, err := Convert([]byte(fmt.Sprintf(mcpPreFunctionPolicy, false)), Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), `MCP server "toolset-a" is conversion-only`)
	conversionErr, ok := AsConversionError(err)
	require.True(t, ok)
	require.Equal(t, "policies", conversionErr.Diagnostics[0].Field)

	// A global pre-function is a top-level plugin, not on the route, so it
	// does not collide with the gate.
	out, warnings := convertMCP(t, fmt.Sprintf(mcpPreFunctionPolicy, true))
	require.Empty(t, warnings)
	require.Equal(t, []string{"ai-mcp-proxy", "pre-function"}, pluginNames(t, out, "toolset-a"))
	require.Equal(t, []string{aimap.MCPToolsetGateTag}, routePlugins(t, out, "toolset-a")[1].Tags)
}

// mcpUnexposedPreFunctionPolicy gives a conversion-only server that no
// listener names a policy of the gate's own plugin type.
const mcpUnexposedPreFunctionPolicy = `
policies:
  - name: my-pre-function
    type: pre-function
    config:
      access: ['kong.log.notice("hello")']
mcp_servers:
  - type: conversion-only
    name: orphan
    config:
      route: {paths: [/mcp/orphan]}
    policies: [my-pre-function]
    tools:
      - {name: report, description: Get a report, method: GET, path: /report}
`

func TestUnexposedConversionOnlySourceWithPreFunctionPolicyIsDropped(t *testing.T) {
	// An unexposed server is pruned, never gated, so its pre-function policy
	// has no gate to collide with: it is dropped with a warning, not rejected.
	out, warnings := convertMCP(t, mcpUnexposedPreFunctionPolicy)
	require.Empty(t, serviceNames(out))
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `MCP server "orphan" is conversion-only but no listener names it`)

	// Under -strict the drop is what fails, not a gate collision.
	_, _, err := Convert([]byte(mcpUnexposedPreFunctionPolicy), Options{Strict: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no listener names it")
	require.NotContains(t, err.Error(), "cannot also have")
}

func TestSourceSharedByListenersWithDifferentAccess(t *testing.T) {
	// Nothing is copied from either listener, so their differing access no
	// longer conflicts: each keeps its own, and the shared source gets one gate.
	out, warnings := convertMCP(t, mcpConflictingListeners)
	require.Empty(t, warnings)

	require.Equal(t, []string{"ai-mcp-proxy", "pre-function"}, pluginNames(t, out, "shared"))
	require.Equal(t,
		map[string]any{"key_names": []any{"apikey"}, "hide_credentials": false},
		routePlugins(t, out, "first")[1].Config)
	require.Equal(t,
		map[string]any{"key_names": []any{"other-key"}, "hide_credentials": false},
		routePlugins(t, out, "second")[1].Config)

	_, _, err := Convert([]byte(mcpConflictingListeners), Options{Strict: true})
	require.NoError(t, err)
}

// mcpHiddenCredentials has a listener whose key-auth strategy asks to hide the
// credential -- which an MCP listener cannot afford to do.
const mcpHiddenCredentials = `
auth_strategies:
  - name: mcp-key-auth
    type: key-auth
    config:
      key_names: [apikey]
      hide_credentials: true
mcp_servers:
  - type: conversion-only
    name: toolset-a
    config:
      route: {paths: [/mcp/a]}
    tools:
      - {name: report-a, description: Get a report, method: GET, path: /report}
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      sources: [toolset-a]
    access:
      auth_strategies: [mcp-key-auth]
`

func TestMCPKeyAuthNeverHidesCredentials(t *testing.T) {
	out, warnings := convertMCP(t, mcpHiddenCredentials)
	require.Empty(t, warnings)

	// Forced to false on the listener -- a strategy asking to hide it does
	// not win here.
	keyAuth := routePlugins(t, out, "aggregate")[1]
	require.Equal(t, "key-auth", keyAuth.Name)
	require.Equal(t, false, keyAuth.Config["hide_credentials"])
}

// mcpUnexposedSources has a conversion-only server no listener names, next to
// one that a listener does.
const mcpUnexposedSources = `
mcp_servers:
  - type: conversion-only
    name: orphan
    config:
      route: {paths: [/mcp/orphan]}
    tools:
      - {name: report, description: Get a report, method: GET, path: /report}
  - type: conversion-only
    name: exposed
    config:
      route: {paths: [/mcp/exposed]}
    tools:
      - {name: report, description: Get a report, method: GET, path: /report}
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      sources: [exposed]
`

func TestUnexposedConversionOnlySourcesAreDropped(t *testing.T) {
	out, warnings := convertMCP(t, mcpUnexposedSources)

	require.Equal(t, []string{"exposed", "aggregate"}, serviceNames(out))
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `MCP server "orphan" is conversion-only but no listener names it`)

	// A source a listener does name survives even though that listener
	// declares no access: association is the test, not the auth plugin.
	require.Equal(t, []string{"ai-mcp-proxy", "pre-function"}, pluginNames(t, out, "exposed"))

	// The drop is a lost entity, so -strict refuses it rather than warning.
	_, _, err := Convert([]byte(mcpUnexposedSources), Options{Strict: true})
	require.Error(t, err)
}

// mcpNameCollidesWithModelService names a conversion-only MCP server after the
// shared model service (aimap.GatewayServiceName), which nothing forbids.
const mcpNameCollidesWithModelService = `
models:
  - name: openai-gpt
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config:
      route: {paths: [/ai]}
    targets:
      - name: gpt-5
        provider: openai-prod
        config: {type: openai}
model_providers:
  - name: openai-prod
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "{vault://ai/t}"}]
mcp_servers:
  - type: conversion-only
    name: ai-gateway
    config:
      route: {paths: [/mcp/x]}
    tools:
      - {name: report, description: Get a report, method: GET, path: /report}
`

func TestPruneRemovesOnlyTheMCPServersOwnService(t *testing.T) {
	out, warnings := convertMCP(t, mcpNameCollidesWithModelService)

	// The unexposed MCP server goes. The model service that happens to share
	// its name stays, with its routes, or every model route would be dropped
	// and the top-level model plugins would point at nothing.
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `MCP server "ai-gateway" is conversion-only`)
	require.Equal(t, []string{"ai-gateway"}, serviceNames(out))
	require.Len(t, out.Services, 1)
	require.NotEmpty(t, out.Services[0].Routes, "the model service kept its routes")
	for _, rt := range out.Services[0].Routes {
		require.NotContains(t, rt.Paths, "/mcp/x", "the MCP route is the one that goes")
	}
}

func TestTokenVaultLowersIntoAuthRecord(t *testing.T) {
	out, _ := convertMCP(t, `
mcp_servers:
  - type: passthrough-listener
    name: vaulted
    upstream_url: https://mcp.internal
    token_vault:
      directory: my-directory
      provider: my-upstream-provider
      encryption_secrets: ["{vault://env/enc}"]
      redis: {host: redis.internal, port: 6379, database: 0}
    tools:
      - {name: report, description: Get a report}
`)
	plugins := routePlugins(t, out, "vaulted")
	require.Len(t, plugins, 1)
	require.Equal(t, map[string]any{
		"provider": "token_vault",
		"token_vault": map[string]any{
			"directory":          "my-directory",
			"provider":           "my-upstream-provider",
			"encryption_secrets": []string{"{vault://env/enc}"},
			"redis": map[string]any{
				"host":     "redis.internal",
				"port":     6379,
				"database": 0,
			},
		},
	}, plugins[0].Config["auth"])
}

func TestTokenVaultConflictsWithUpstreamAuth(t *testing.T) {
	doc, err := aigw.Parse([]byte(`
mcp_servers:
  - type: passthrough-listener
    name: both
    upstream_url: https://mcp.internal
    token_vault:
      directory: my-directory
      provider: my-upstream-provider
    config:
      upstream:
        auth: {type: aws, region: us-west-2}
    tools:
      - {name: report, description: Get a report}
`))
	require.NoError(t, err)
	_, _, err = ConvertDocument(doc, Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestTokenVaultRedisRequiresEncryptionSecrets(t *testing.T) {
	doc, err := aigw.Parse([]byte(`
mcp_servers:
  - type: passthrough-listener
    name: no-secrets
    upstream_url: https://mcp.internal
    token_vault:
      directory: my-directory
      provider: my-upstream-provider
      redis: {host: redis.internal, port: 6379}
    tools:
      - {name: report, description: Get a report}
`))
	require.NoError(t, err)
	_, _, err = ConvertDocument(doc, Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "encryption_secrets is required")
}

// A present but empty token_vault block (or one missing its required
// directory/provider) would lower to auth.token_vault: {} — the plugin schema
// rejects that, so convert must too, rather than emitting it silently.
func TestTokenVaultRequiresDirectoryAndProvider(t *testing.T) {
	for _, tt := range []struct {
		name  string
		block string
	}{
		{name: "empty block", block: "    token_vault: {}"},
		{name: "missing provider", block: "    token_vault:\n      directory: my-directory"},
		{name: "missing directory", block: "    token_vault:\n      provider: my-upstream-provider"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := aigw.Parse([]byte(`
mcp_servers:
  - type: passthrough-listener
    name: hollow
    upstream_url: https://mcp.internal
` + tt.block + `
    tools:
      - {name: report, description: Get a report}
`))
			require.NoError(t, err)
			_, _, err = ConvertDocument(doc, Options{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "token_vault requires directory and provider")
		})
	}
}
