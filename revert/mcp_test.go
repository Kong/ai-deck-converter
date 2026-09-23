package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/convert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// mcpGatedSourceDeck is what the forward converter emits for a key-auth
// protected listener over one conversion-only source: the listener carries the
// key-auth, the source only the generated toolset gate, joined by the
// listener's bucket tag.
const mcpGatedSourceDeck = `
_format_version: "3.0"
services:
  - name: toolset-a
    host: localhost
    routes:
      - name: toolset-a-route
        paths: [/mcp/a]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: conversion-only
              tools:
                - name: report-a
                  description: Get a report
                  method: GET
                  path: /report
            tags: [mcp-listener:aggregate-id]
          - name: pre-function
            config:
              access:
                - |
                  local addr = ngx.var.server_addr or ""
                  if addr:sub(1, 5) ~= "unix:" then
                    return ngx.exit(ngx.HTTP_NOT_FOUND)
                  end
            tags: [aigw-generated:mcp-toolset-gate]
  - name: aggregate
    host: localhost
    routes:
      - name: aggregate-route
        paths: [/mcp/aggregate]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
              server:
                tag: mcp-listener:aggregate-id
          - name: key-auth
            config:
              key_names: [apikey]
`

type revertedMCP struct {
	AuthStrategies []struct {
		Name string `yaml:"name"`
	} `yaml:"auth_strategies"`
	MCPServers []struct {
		Name   string `yaml:"name"`
		Type   string `yaml:"type"`
		Access struct {
			AuthStrategies []string `yaml:"auth_strategies"`
		} `yaml:"access"`
		Policies []string `yaml:"policies"`
	} `yaml:"mcp_servers"`
}

func revertMCPDoc(t *testing.T, deck string) revertedMCP {
	t.Helper()
	out, warnings, err := Revert([]byte(deck), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	var doc revertedMCP
	require.NoError(t, yaml.Unmarshal(out, &doc))
	return doc
}

func TestToolsetGateIsNotAPolicy(t *testing.T) {
	doc := revertMCPDoc(t, mcpGatedSourceDeck)
	require.Len(t, doc.MCPServers, 2)

	byName := map[string]int{}
	for i, m := range doc.MCPServers {
		byName[m.Name] = i
	}
	source := doc.MCPServers[byName["toolset-a"]]
	listener := doc.MCPServers[byName["aggregate"]]

	// The gate is derived from the conversion-only type, so it comes back as
	// nothing at all; convert puts it back on the route.
	require.Equal(t, "conversion-only", source.Type)
	require.Empty(t, source.Access.AuthStrategies)
	require.Empty(t, source.Policies, "the generated gate must not become a policy")
	require.Len(t, listener.Access.AuthStrategies, 1)
}

// mcpSourceOwnAuthDeck is a hand-written config: the conversion-only source
// carries a key-auth of its own. (Configs from older converter versions, which
// copied the listener's access onto its sources, have the same shape.)
const mcpSourceOwnAuthDeck = `
_format_version: "3.0"
services:
  - name: toolset-a
    host: localhost
    routes:
      - name: toolset-a-route
        paths: [/mcp/a]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: conversion-only
            tags: [mcp-listener:aggregate-id]
          - name: key-auth
            config:
              key_names: [source-only-key]
  - name: aggregate
    host: localhost
    routes:
      - name: aggregate-route
        paths: [/mcp/aggregate]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
              server:
                tag: mcp-listener:aggregate-id
          - name: key-auth
            config:
              key_names: [apikey]
`

func TestSourceOwnAuthBecomesAPolicyNotAccess(t *testing.T) {
	out, warnings, err := Revert([]byte(mcpSourceOwnAuthDeck), Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)

	var doc struct {
		Policies []struct {
			Name string `yaml:"name"`
			Type string `yaml:"type"`
		} `yaml:"policies"`
		MCPServers []struct {
			Name   string `yaml:"name"`
			Type   string `yaml:"type"`
			Access struct {
				AuthStrategies []string `yaml:"auth_strategies"`
			} `yaml:"access"`
			Policies []string `yaml:"policies"`
		} `yaml:"mcp_servers"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc))

	byName := map[string]int{}
	for i, m := range doc.MCPServers {
		byName[m.Name] = i
	}
	source := doc.MCPServers[byName["toolset-a"]]

	// Auth a listener did not propagate is still the source's own, but it
	// cannot come back as access: only listener modes may declare that. A
	// policy carries it instead, and convert puts a policy back on this same
	// route.
	require.Equal(t, "conversion-only", source.Type)
	require.Empty(t, source.Access.AuthStrategies)
	require.Len(t, source.Policies, 1)
	require.Len(t, doc.Policies, 1)
	require.Equal(t, "key-auth", doc.Policies[0].Type)

	// The point of all of it: the reverted document converts. Lifting the
	// plugin into access.auth_strategies made this a hard error.
	_, _, err = convert.Convert(out, convert.Options{})
	require.NoError(t, err)
}

// mcpHandWrittenPreFunctionDeck is a hand-written config: the conversion-only
// source carries a pre-function without the gate's tag.
const mcpHandWrittenPreFunctionDeck = `
_format_version: "3.0"
services:
  - name: toolset-a
    host: localhost
    routes:
      - name: toolset-a-route
        paths: [/mcp/a]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: conversion-only
              tools:
                - name: report-a
                  description: Get a report
                  method: GET
                  path: /report
            tags: [mcp-listener:aggregate-id]
          - name: pre-function
            config:
              access:
                - kong.log.notice("hello")
  - name: aggregate
    host: localhost
    routes:
      - name: aggregate-route
        paths: [/mcp/aggregate]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
              server:
                tag: mcp-listener:aggregate-id
`

func TestUntaggedPreFunctionOnSourceIsAPolicy(t *testing.T) {
	doc := revertMCPDoc(t, mcpHandWrittenPreFunctionDeck)

	byName := map[string]int{}
	for i, m := range doc.MCPServers {
		byName[m.Name] = i
	}
	source := doc.MCPServers[byName["toolset-a"]]

	// Only the tagged gate is generated; a user's own pre-function survives.
	require.Equal(t, "conversion-only", source.Type)
	require.Len(t, source.Policies, 1)

	// Faithful, but not convertible: convert would have to put the gate next
	// to this policy, and Kong allows one pre-function per route, so it refuses
	// rather than emit a config Kong rejects.
	out, _, err := Revert([]byte(mcpHandWrittenPreFunctionDeck), Options{})
	require.NoError(t, err)
	_, _, err = convert.Convert(out, convert.Options{})
	require.ErrorContains(t, err, `MCP server "toolset-a" is conversion-only`)
}
