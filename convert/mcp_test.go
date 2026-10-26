package convert

import (
	"fmt"
	"slices"
	"testing"

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
	out, warnings, err := convertDoc([]byte(src), false)
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
	result, err := Convert([]byte(fmt.Sprintf(mcpPreFunctionPolicy, false)), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindMCPServer, "toolset-a", "policies")
	require.Contains(t, issue.Reason, `MCP server "toolset-a" is conversion-only`)

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
	_, _, err := convertDoc([]byte(mcpUnexposedPreFunctionPolicy), true)
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

	_, _, err := convertDoc([]byte(mcpConflictingListeners), true)
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
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "hide_credentials will be overridden to false")

	// Forced to false on the listener -- a strategy asking to hide it does
	// not win here.
	keyAuth := routePlugins(t, out, "aggregate")[1]
	require.Equal(t, "key-auth", keyAuth.Name)
	require.Equal(t, false, keyAuth.Config["hide_credentials"])

	_, _, err := convertDoc([]byte(mcpHiddenCredentials), true)
	require.Error(t, err)
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
	_, _, err := convertDoc([]byte(mcpUnexposedSources), true)
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
datastores:
  - name: tv
    type: token-vault
    config: {directory: my-directory}
mcp_servers:
  - type: passthrough-listener
    name: vaulted
    upstream_url: https://mcp.internal
    datastores: [{name: tv}]
    token_vault:
      provider: my-upstream-provider
    tools:
      - {name: report, description: Get a report}
`)
	plugins := routePlugins(t, out, "vaulted")
	require.Len(t, plugins, 1)
	require.Equal(t, map[string]any{
		"provider": "token_vault",
		"token_vault": map[string]any{
			"directory": "my-directory",
			"provider":  "my-upstream-provider",
			"storage":   map[string]any{"backend": "konnect"},
		},
	}, plugins[0].Config["auth"])
}

func TestTokenVaultConflictsWithUpstreamAuth(t *testing.T) {
	_, _, err := convertDoc([]byte(`
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
`), false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

// A present but empty token_vault block (or one missing its required
// provider) would lower to an invalid auth.token_vault — the plugin schema
// rejects that, so convert must too, rather than emitting it silently.
func TestTokenVaultRequiresProvider(t *testing.T) {
	for _, tt := range []struct {
		name  string
		block string
	}{
		{name: "empty block", block: "    token_vault: {}"},
		{name: "missing provider", block: "    token_vault:\n      subject_token_type: api_key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := convertDoc([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: my-directory}
mcp_servers:
  - type: passthrough-listener
    name: hollow
    upstream_url: https://mcp.internal
    datastores: [{name: tv}]
`+tt.block+`
    tools:
      - {name: report, description: Get a report}
`), false)
			require.Error(t, err)
			require.Contains(t, err.Error(), "token_vault requires provider")
		})
	}
}

// The AI Gateway API accepts rate_limiting only on conversion-listener and
// passthrough-listener.
func TestMCPRateLimitingRejectedOnOtherTypes(t *testing.T) {
	for _, mode := range []string{"listener", "conversion-only", "upstream-server"} {
		t.Run(mode, func(t *testing.T) {
			result, err := Convert([]byte(`
mcp_servers:
  - type: `+mode+`
    name: limited
    upstream_url: https://mcp.internal
    rate_limiting:
      limits:
        - {method: tools/call, limit: 100, window_size: 60}
`), testOptions)
			require.NoError(t, err)
			requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindMCPServer, "limited", "rate_limiting")
			require.Empty(t, result.Output.Services, "the MCP server is left out")
		})
	}
}

// mcpWhenACLInput templates an MCP server whose ACL block is supplied by the
// caller, so one fixture covers the server-wide and per-tool placements.
//
//nolint:gosec
const mcpWhenACLInput = `
mcp_servers:
  - type: listener
    display_name: Guarded MCP
    name: guarded-mcp
    config:
      route:
        paths: [/mcp/guarded]
%s
    tools:
      - name: echo
        description: echo
        method: GET
        path: /anything
        scheme: https
        host: httpbin.internal
%s
`

// TestMCPACLWhenExpressionsRejected pins the schema boundary: ai-mcp-proxy's
// default_acl entries and tools[].acl records define only scope/allow/deny, so
// a CEL when-expression cannot be represented there. A server-wide expression
// leaves out the MCP server. A per-tool expression leaves out only the tool.
func TestMCPACLWhenExpressionsRejected(t *testing.T) {
	cases := []struct {
		name     string
		access   string
		tool     string
		severity ConversionIssueSeverity
		field    string
	}{
		{
			name: "server acls allow_when",
			access: `    access:
      acls:
        allow_when: ["principal.metadata.admin == 'true'"]`,
			severity: Error,
			field:    "access.acls.allow_when",
		},
		{
			name: "server default_tool_acls deny_when",
			access: `    access:
      default_tool_acls:
        deny_when: ["principal.metadata.admin != 'true'"]`,
			severity: Error,
			field:    "access.default_tool_acls.deny_when",
		},
		{
			name: "per-tool acls allow_when",
			tool: `        access:
          acls:
            allow_when: ["principal.metadata.admin == 'true'"]`,
			severity: Warning,
			field:    "tools[0].access.acls.allow_when",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(fmt.Sprintf(mcpWhenACLInput, tc.access, tc.tool))
			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			issue := requireIssue(t, result.Issues, tc.severity, ConfigFieldInvalid, kindMCPServer, "guarded-mcp",
				tc.field)
			require.Contains(t, issue.Reason, "allow_when/deny_when")
			if tc.severity == Error {
				require.Empty(t, result.Output.Services, "the MCP server is left out")
				return
			}
			require.Len(t, result.Output.Services, 1, "the MCP server still converts")
			plugin := result.Output.Services[0].Routes[0].Plugins[0]
			require.Empty(t, plugin.Config["tools"], "the tool is left out")
		})
	}
}

// TestMCPACLAllowDenyStillConverts guards the gate's blast radius: a plain
// allow/deny ACL is the shape ai-mcp-proxy does accept and must keep working.
func TestMCPACLAllowDenyStillConverts(t *testing.T) {
	access := `    access:
      acls:
        allow: [premium-users]`
	tool := `        access:
          acls:
            deny: [banned-users]`
	result, err := Convert([]byte(fmt.Sprintf(mcpWhenACLInput, access, tool)), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues, "allow/deny ACLs must still convert")
	require.Len(t, result.Output.Services, 1)
}

func TestTokenVaultDatastore(t *testing.T) {
	const doc = `
datastores:
  - name: tv
    type: %s
    config:
      directory: my-directory
      current_version: v1
mcp_servers:
  - type: passthrough-listener
    name: gh
    upstream_url: https://mcp.internal
    datastores: [%s]
    token_vault:
      provider: github
    tools:
      - {name: report, description: Get a report}
`
	t.Run("resolved", func(t *testing.T) {
		out, _, err := convertDoc([]byte(fmt.Sprintf(doc, "token-vault", "{name: tv}")), false)
		require.NoError(t, err)
		y, err := out.ToYAML()
		require.NoError(t, err)
		require.Contains(t, string(y), "directory: my-directory")
		require.Contains(t, string(y), "storage:")
		require.Contains(t, string(y), "current_version: v1")
		require.Contains(t, string(y), "backend: konnect")
	})
	t.Run("wrong type", func(t *testing.T) {
		_, _, err := convertDoc([]byte(fmt.Sprintf(doc, "redis-ee", "{name: tv}")), false)
		require.Error(t, err)
	})
	t.Run("unknown name", func(t *testing.T) {
		_, _, err := convertDoc([]byte(fmt.Sprintf(doc, "token-vault", "{name: nope}")), false)
		require.Error(t, err)
	})
}

func TestTokenVaultCallbackRoutesAddedOnce(t *testing.T) {
	server := func(name string) string {
		return `
  - type: passthrough-listener
    name: ` + name + `
    upstream_url: https://mcp.internal
    datastores: [{name: tv}]
    token_vault: {provider: github}
    tools:
      - {name: report, description: Get a report}`
	}
	out, _, err := convertDoc([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: d, current_version: v1}
mcp_servers:`+server("a")+server("b")+"\n"), false)
	require.NoError(t, err)
	require.Len(t, out.Routes, 1)
	require.Equal(t, aimap.TokenVaultCallbackName, out.Routes[0].Name)
	require.Equal(t, "d", out.Routes[0].Plugins[0].Config["directory"])

	dbless := out.ToDBLess()
	require.Len(t, dbless.Routes, 3)
	require.Len(t, dbless.Plugins, 3)
	callbackRoute := dbless.Routes[2]
	require.Equal(t, aimap.TokenVaultCallbackName, callbackRoute.Name)
	require.Equal(t, []string{aimap.TokenVaultCallbackPath}, callbackRoute.Paths)
	require.NotEmpty(t, callbackRoute.ID)
	require.Equal(t, []string{"http", "https"}, callbackRoute.Protocols)
	require.Nil(t, callbackRoute.Service, "a callback route has no service")
	for _, route := range dbless.Routes[:2] {
		require.NotNil(t, route.Service)
		require.NotEqual(t, callbackRoute.ID, route.ID)
	}
	callbackPlugin := dbless.Plugins[2]
	require.Equal(t, aimap.TokenVaultCallbackName, callbackPlugin.Name)
	require.Equal(t, map[string]string{"id": callbackRoute.ID}, callbackPlugin.Route)
	require.Nil(t, callbackPlugin.Service)
	require.Equal(t, out.Routes[0].Plugins[0].Config, callbackPlugin.Config)

	deckMeta := out.Metadata()
	var deckRoute *kong.GeneratedEntitySource
	for i, r := range deckMeta.Routes {
		if r.Location == "routes[0]" {
			deckRoute = &deckMeta.Routes[i]
		}
	}
	require.NotNil(t, deckRoute, "decK metadata records the top-level callback route")
	require.Equal(t, "a", deckRoute.EntityName)
	var deckPlugin bool
	for _, p := range deckMeta.Plugins {
		deckPlugin = deckPlugin || p.Location == "routes[0].plugins[0]"
	}
	require.True(t, deckPlugin, "decK metadata records the callback plugin")

	var dbRoute, dbPlugin bool
	dbMeta := dbless.Metadata()
	for _, r := range dbMeta.Routes {
		dbRoute = dbRoute || r.Location == "routes[2]"
	}
	for _, p := range dbMeta.Plugins {
		dbPlugin = dbPlugin || p.Location == "plugins[2]"
	}
	require.True(t, dbRoute, "db-less metadata records the callback route")
	require.True(t, dbPlugin, "db-less metadata records the callback plugin")
}

func TestTokenVaultUnknownDatastoreFailsServer(t *testing.T) {
	_, _, err := convertDoc([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
mcp_servers:
  - type: passthrough-listener
    name: vaulted
    upstream_url: https://mcp.internal
    datastores: [{name: nope}]
    token_vault: {provider: github}
    tools:
      - {name: report, description: Get a report}
`), false)
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown datastore "nope"`)
}

func TestTokenVaultWithoutDatastoreKeepsServer(t *testing.T) {
	result, err := Convert([]byte(`
mcp_servers:
  - type: passthrough-listener
    name: vaulted
    upstream_url: https://mcp.internal
    token_vault: {directory: default, provider: github}
    tools:
      - {name: report, description: Get a report}
`), testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	requireIssue(t, result.Issues, Warning, MissingRequiredConfigField, kindMCPServer, "vaulted", "datastores")
	require.Empty(t, result.Output.Routes, "no Token Vault callback route")
	require.Len(t, result.Output.Services, 1)
	require.Len(t, result.Output.Services[0].Routes, 1)
	plugins := result.Output.Services[0].Routes[0].Plugins
	i := slices.IndexFunc(plugins, func(p kong.Plugin) bool { return p.Name == "ai-mcp-proxy" })
	require.GreaterOrEqual(t, i, 0)
	require.Equal(t, map[string]any{
		"provider":    "token_vault",
		"token_vault": map[string]any{"directory": "default", "provider": "github"},
	}, plugins[i].Config["auth"])
}

func TestTokenVaultCallbackRoutePrunedWithUnexposedServer(t *testing.T) {
	const datastore = `
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
mcp_servers:
  - type: conversion-only
    name: hidden
    config:
      route: {name: hidden-route, paths: [/mcp/hidden]}
    datastores: [{name: tv}]
    token_vault: {provider: github}
    tools:
      - {name: report, description: Get a report, method: GET, path: /report}
`
	t.Run("no listener names the server", func(t *testing.T) {
		out, _, err := convertDoc([]byte(datastore), false)
		require.NoError(t, err)
		require.Empty(t, out.Services)
		require.Empty(t, out.Routes)
	})
	t.Run("another server keeps the route", func(t *testing.T) {
		out, _, err := convertDoc([]byte(datastore+`  - type: passthrough-listener
    name: shown
    upstream_url: https://mcp.internal
    datastores: [{name: tv}]
    token_vault: {provider: github}
    tools:
      - {name: report, description: Get a report}
`), false)
		require.NoError(t, err)
		require.Len(t, out.Services, 1)
		requireCallbackRoutes(t, out.Routes)
	})
}

// The Token Vault plugins require a directory. An inline
// token_vault.directory does not replace the datastore directory.
func TestTokenVaultDatastoreWithoutDirectory(t *testing.T) {
	for name, tokenVault := range map[string]string{
		"no inline directory": "{provider: github}",
		"inline directory":    "{directory: inline, provider: github}",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Convert([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {current_version: v1}
mcp_servers:
  - type: passthrough-listener
    name: vaulted
    upstream_url: https://mcp.internal
    datastores: [{name: tv}]
    token_vault: `+tokenVault+`
    tools:
      - {name: report, description: Get a report}
`), testOptions)
			require.NoError(t, err)
			issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid,
				kindMCPServer, "vaulted", "datastores")
			require.Contains(t, issue.Reason, "requires config.directory")
			require.Empty(t, result.Output.Services)
			require.Empty(t, result.Output.Routes, "no callback route without a directory")
		})
	}
}

// requireCallbackRoutes asserts that routes holds exactly the given Token
// Vault callback route names, in order. Without names it expects the one
// open /_vault route.
func requireCallbackRoutes(t *testing.T, routes []kong.Route, want ...string) {
	t.Helper()
	if len(want) == 0 {
		want = []string{aimap.TokenVaultCallbackName}
	}
	names := make([]string, len(routes))
	for i, route := range routes {
		names[i] = route.Name
	}
	require.Equal(t, want, names)
}

// A referenced token-vault datastore overrides the server's inline
// token_vault.directory with a warning.
func TestTokenVaultDatastoreOverridesInlineDirectory(t *testing.T) {
	result, err := Convert([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: from-datastore}
mcp_servers:
  - type: passthrough-listener
    name: vaulted
    upstream_url: https://mcp.internal
    datastores: [{name: tv}]
    token_vault: {directory: inline, provider: github}
    tools:
      - {name: report, description: Get a report}
`), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Warning, ConfigFieldIgnored,
		kindMCPServer, "vaulted", "token_vault.directory")
	require.Contains(t, issue.Reason, `"tv"`)
	auth := routePlugins(t, result.Output, "vaulted")[0].Config["auth"].(map[string]any)
	require.Equal(t, "from-datastore", auth["token_vault"].(map[string]any)["directory"])
}
