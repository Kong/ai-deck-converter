package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestConvertMCPToolWithoutHostGetsCompanionRoute covers the conversion-mode
// dispatch bug: a conversion-listener tool with no per-tool host override is
// meant to dispatch against its MCP server's own Gateway Service (per
// AIGatewayMCPConversionTool's own doc string: "By default, Kong will extract
// the host from API configuration"). At runtime, ai-mcp-proxy resolves that by
// re-entering Kong's own router with the tool's raw method+path, which only
// works if some Route actually matches that path. Without a companion Route
// keyed on the tool's own path, that self-dispatch 404s (see
// spec-ee/kong-api-tests/test/gateway/ai-deck-converter/21_generated_mcp_conversion_dispatch
// for the end-to-end evidence). This shape mirrors that scenario's input.yaml.
func TestConvertMCPToolWithoutHostGetsCompanionRoute(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    display_name: Echo MCP
    name: echo-mcp
    upstream_url: https://echo.example.com
    config:
      route: {name: echo-mcp-route, paths: [/mcp/echo]}
    tools:
      - name: getAnything
        description: Fetch a resource from the wrapped REST backend
        method: GET
        path: /anything
      - name: getResource
        description: Fetch a resource by id from the wrapped REST backend
        method: GET
        path: /resource/{id}
      - name: getExternal
        description: Dials an external host directly, not this server's own service
        method: GET
        path: /external
        scheme: https
        host: external.internal
`)
	out, warnings, err := Convert(src, Options{})
	require.NoError(t, err, "convert")
	require.Empty(t, warnings, "unexpected warnings")

	type route struct {
		Name      string   `yaml:"name"`
		Paths     []string `yaml:"paths"`
		Methods   []string `yaml:"methods"`
		StripPath *bool    `yaml:"strip_path"`
		Tags      []string `yaml:"tags"`
	}
	var got struct {
		Services []struct {
			Name   string  `yaml:"name"`
			Routes []route `yaml:"routes"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Services, 1, "expected exactly one service, got %+v", got.Services)

	svc := got.Services[0]
	require.Equal(t, "echo-mcp", svc.Name)
	require.Len(t, svc.Routes, 3,
		"expected the listener route plus one companion route per host-less tool, got %+v", svc.Routes)

	byName := map[string]route{}
	for _, rt := range svc.Routes {
		byName[rt.Name] = rt
	}

	require.Contains(t, byName, "echo-mcp-route", "listener route must still be present")

	anything, ok := byName["echo-mcp-getAnything-route"]
	require.Truef(t, ok, "expected a companion route for getAnything, got routes %+v", byName)
	require.Equal(t, []string{"/anything"}, anything.Paths, "companion route path")
	require.Equal(t, []string{"GET"}, anything.Methods, "companion route must be scoped to the tool's method")
	require.NotNilf(t, anything.StripPath, "companion route must set strip_path explicitly")
	require.False(t, *anything.StripPath, "companion route must set strip_path: false or the matched prefix gets stripped before proxying")
	require.Equal(t, []string{"ai-deck-converter:mcp-tool-route"}, anything.Tags,
		"companion route must carry the converter-provenance marker tag so revert can recognize it")

	resource, ok := byName["echo-mcp-getResource-route"]
	require.Truef(t, ok, "expected a companion route for getResource, got routes %+v", byName)
	require.Equal(t, []string{"/resource"}, resource.Paths,
		"templated path /resource/{id} must be truncated to its prefix /resource")
	require.Equal(t, []string{"GET"}, resource.Methods)

	require.NotContainsf(t, byName, "echo-mcp-getExternal-route",
		"a tool with a host override dials that host directly and must not get a companion route, got %+v", byName)
}

// TestConvertMCPToolCompanionRouteScopedToConversionModes verifies the
// companion-route fix is scoped to conversion-only and conversion-listener
// (the only MCP server types whose tools use AIGatewayMCPConversionTool and
// are dispatched via ai-mcp-proxy's own-Service self-loop). passthrough-listener
// forwards straight through to its own already-matching route and never
// synthesizes a per-tool HTTP call, so a host-less absolute tool path there
// must not get a companion route either.
func TestConvertMCPToolCompanionRouteScopedToConversionModes(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: passthrough-listener
    name: vendor-mcp
    upstream_url: https://vendor-mcp.example.com/mcp
    config:
      route: {paths: [/mcp/vendor]}
    tools:
      - name: someTool
        description: A tool identifier used only for ACL enforcement in this mode
        method: GET
        path: /anything
`)
	out, _, err := Convert(src, Options{})
	require.NoError(t, err, "convert")

	var got struct {
		Services []struct {
			Name   string `yaml:"name"`
			Routes []struct {
				Name string `yaml:"name"`
			} `yaml:"routes"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Services, 1)
	require.Len(t, got.Services[0].Routes, 1,
		"passthrough-listener must not get a companion route for its tools, got %+v", got.Services[0].Routes)
}
