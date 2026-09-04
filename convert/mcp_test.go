package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
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

func TestListenerAccessPropagatesToConversionOnlySources(t *testing.T) {
	out, warnings := convertMCP(t, mcpListenerWithAccess)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `MCP server "unexposed" is conversion-only but no listener names it`)

	// The exposed sources are protected on the listener's terms; the listener
	// keeps its own plugin; an unexposed source gets nothing to copy.
	require.Equal(t, []string{"ai-mcp-proxy", "key-auth"}, pluginNames(t, out, "toolset-a"))
	require.Equal(t, []string{"ai-mcp-proxy", "key-auth"}, pluginNames(t, out, "toolset-b"))
	require.Equal(t, []string{"ai-mcp-proxy", "key-auth"}, pluginNames(t, out, "aggregate"))
	// "unexposed" is named by no listener, so it is dropped rather than
	// published as an endpoint nothing protects.
	require.NotContains(t, serviceNames(out), "unexposed")

	// The propagated plugin carries the listener's config, in its own map so
	// the two routes cannot alias one another.
	listener := routePlugins(t, out, "aggregate")[1]
	source := routePlugins(t, out, "toolset-a")[1]
	require.Equal(t, listener.Config, source.Config)
	require.Equal(t, map[string]any{"key_names": []any{"apikey"}, "hide_credentials": false}, source.Config)
	source.Config["key_names"] = []any{"changed"}
	require.Equal(t, []any{"apikey"}, listener.Config["key_names"])
}

func TestListenerWithoutAccessLeavesSourcesOpen(t *testing.T) {
	// No access on the listener: nothing to propagate, and no plugin is
	// invented for the source.
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
	require.Equal(t, []string{"ai-mcp-proxy"}, pluginNames(t, out, "toolset-a"))
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

func TestConflictingListenerAccessWarnsAndKeepsFirst(t *testing.T) {
	out, warnings := convertMCP(t, mcpConflictingListeners)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `MCP server "shared" is exposed by listeners with conflicting "key-auth" access`)
	require.Contains(t, warnings[0], `ignoring listener "second"`)

	// Only one plugin per name may live on a route: the first listener wins.
	require.Equal(t, []string{"ai-mcp-proxy", "key-auth"}, pluginNames(t, out, "shared"))
	require.Equal(t,
		map[string]any{"key_names": []any{"apikey"}, "hide_credentials": false},
		routePlugins(t, out, "shared")[1].Config)

	// In strict mode the conflict is an error rather than a warning.
	_, _, err := Convert([]byte(mcpConflictingListeners), Options{Strict: true})
	require.Error(t, err)
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

	// Forced to false on the listener, and on the copy propagated to the
	// source's route -- a strategy asking to hide it does not win here.
	for _, service := range []string{"aggregate", "toolset-a"} {
		keyAuth := routePlugins(t, out, service)[1]
		require.Equal(t, "key-auth", keyAuth.Name, service)
		require.Equal(t, false, keyAuth.Config["hide_credentials"], service)
	}
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
	require.Equal(t, []string{"ai-mcp-proxy"}, pluginNames(t, out, "exposed"))

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
