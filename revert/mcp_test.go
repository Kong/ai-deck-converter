package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/convert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// mcpPropagatedAccessDeck is what the forward converter emits for a key-auth
// protected listener over one conversion-only source: the same key-auth plugin
// on both routes, joined by the listener's bucket tag.
const mcpPropagatedAccessDeck = `
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
          - name: key-auth
            config:
              key_names: [apikey]
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

func TestPropagatedListenerAccessIsNotTheSourcesOwn(t *testing.T) {
	doc := revertMCPDoc(t, mcpPropagatedAccessDeck)
	require.Len(t, doc.MCPServers, 2)

	byName := map[string]int{}
	for i, m := range doc.MCPServers {
		byName[m.Name] = i
	}
	source := doc.MCPServers[byName["toolset-a"]]
	listener := doc.MCPServers[byName["aggregate"]]

	// Auth belongs to the listener only. Leaving it on the conversion-only
	// source would not just break the round trip -- re-converting it is a hard
	// error, since only listener modes may declare access.
	require.Equal(t, "conversion-only", source.Type)
	require.Empty(t, source.Access.AuthStrategies)
	require.Empty(t, source.Policies, "propagated access must not become a policy either")
	require.Len(t, listener.Access.AuthStrategies, 1)
	require.Len(t, doc.AuthStrategies, 1, "one auth strategy, not one per route")
}

// mcpSourceOwnAuthDeck is a hand-written config: the conversion-only source
// carries a key-auth no listener shares, so nothing propagated it.
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

// mcpUntaggedListenerDeck is what convert emits for a listener that declares
// sources and access but no config.server.tag: the access plugin is propagated
// to the source's route, but no bucket tag ties the two together.
const mcpUntaggedListenerDeck = `
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
          - name: key-auth
            config:
              key_names: [apikey]
              hide_credentials: false
  - name: aggregate
    host: localhost
    routes:
      - name: aggregate-route
        paths: [/mcp/aggregate]
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: listener
          - name: key-auth
            config:
              key_names: [apikey]
              hide_credentials: false
`

func TestPropagatedAccessStrippedWithoutABucketTag(t *testing.T) {
	doc := revertMCPDoc(t, mcpUntaggedListenerDeck)

	byName := map[string]int{}
	for i, m := range doc.MCPServers {
		byName[m.Name] = i
	}
	source := doc.MCPServers[byName["toolset-a"]]

	// No tag means no listener/source association to rebuild, but the access
	// must still be recognized as propagated. Lifting it onto a
	// conversion-only server produces a document convert rejects outright.
	require.Equal(t, "conversion-only", source.Type)
	require.Empty(t, source.Access.AuthStrategies)
	require.Empty(t, source.Policies)
	require.Len(t, doc.MCPServers[byName["aggregate"]].Access.AuthStrategies, 1)
}
