package convert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestStaticPathPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/flights", "/flights", true},
		{"/flights/book", "/flights/book", true},
		{"/flights/{flightNumber}", "/flights", true},
		{"/users/{userId}/orders/{orderId}", "/users", true},
		{"/flights/", "/flights", true},
		{"/{version}/x", "", false},
		{"/", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := staticPathPrefix(tc.in)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

// mcpRoutePaths returns, for the named service, the paths of any route other
// than the MCP endpoint route (i.e. the synthesized companion route), or nil.
func mcpCompanionPaths(t *testing.T, out []byte, service string) []string {
	t.Helper()
	var doc struct {
		Services []struct {
			Name   string `yaml:"name"`
			Routes []struct {
				Name  string   `yaml:"name"`
				Paths []string `yaml:"paths"`
			} `yaml:"routes"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")
	for _, svc := range doc.Services {
		if svc.Name != service {
			continue
		}
		for _, rt := range svc.Routes {
			if rt.Name == service+"-tools-route" {
				return rt.Paths
			}
		}
	}
	return nil
}

func TestConvertMCPCompanionToolRoute(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: flights-mcp
    config:
      route: {paths: [/mcp/flights]}
    tools:
      - {name: search, description: search, method: GET, path: "/flights/{id}"}
      - {name: book, description: book, method: POST, path: /flights/book}
      - {name: dup, description: dup, method: GET, path: /flights/other}
`)
	out, warnings, err := Convert(src, Options{})
	require.NoError(t, err, "convert")
	require.Empty(t, warnings, "no warnings expected for static-first absolute paths")

	// /flights/{id} -> /flights, /flights/book, /flights/other; sorted, de-duped.
	require.Equal(t, []string{"/flights", "/flights/book", "/flights/other"},
		mcpCompanionPaths(t, out, "flights-mcp"))
}

func TestConvertMCPCompanionRouteTemplateLeadingSegmentWarns(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: m1
    config:
      route: {paths: [/mcp]}
    tools:
      - {name: t, description: a tool, method: GET, path: "/{version}/x"}
`)
	out, warnings, err := Convert(src, Options{})
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "template segment",
		"expected a warning for a leading-template tool path")
	require.Nil(t, mcpCompanionPaths(t, out, "m1"),
		"no companion route should be synthesized for a leading-template path")

	_, _, err = Convert(src, Options{Strict: true})
	require.Error(t, err, "expected strict mode to fail on a leading-template tool path")
}

func TestConvertMCPCompanionRouteRelativePathSkipped(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: m1
    config:
      route: {paths: [/mcp]}
    tools:
      - {name: t, description: a tool, method: GET, path: flights}
`)
	out, warnings, err := Convert(src, Options{})
	require.NoError(t, err, "convert")
	require.Empty(t, warnings, "relative tool paths must not warn")
	require.Nil(t, mcpCompanionPaths(t, out, "m1"),
		"no companion route should be synthesized for a relative tool path")
}

func TestConvertMCPNoCompanionRouteForNonConversionModes(t *testing.T) {
	for _, mode := range []string{"passthrough-listener", "listener", "upstream-server"} {
		t.Run(mode, func(t *testing.T) {
			src := []byte(`
mcp_servers:
  - type: ` + mode + `
    name: m1
    upstream_url: https://upstream.example.com/mcp
    config:
      route: {paths: [/mcp]}
    tools:
      - {name: t, description: a tool, method: GET, path: /flights}
`)
			out, _, err := Convert(src, Options{})
			require.NoError(t, err, "convert")
			require.Nil(t, mcpCompanionPaths(t, out, "m1"),
				"mode %q must not synthesize a companion route", mode)
		})
	}
}
