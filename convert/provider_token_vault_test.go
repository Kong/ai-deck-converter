package convert

import (
	"fmt"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

//nolint:gosec // a test fixture, not a credential
const providerTokenVaultDatastores = `
datastores:
  - name: tv
    type: token-vault
    config:
      directory: d
      current_version: v1
      keys: [{version: v1, key: k}]
  - name: tv-other
    type: token-vault
    config:
      directory: other
      current_version: v1
      keys: [{version: v1, key: k}]
  - name: cache
    type: redis-ee
    config: {host: redis.internal}
`

const vaultProvider = `
  - name: vault
    type: anthropic
    datastores: [{name: tv}]
    token_vault: {provider: anthropic}`

const staticProvider = `
  - name: static
    type: anthropic
    config:
      auth:
        type: basic
        headers: [{name: x-api-key, value: secret}]`

func providerTokenVaultModel(name string, providers ...string) string {
	out := `
  - name: ` + name + `
    type: model
    capabilities: [generate]
    formats: [{type: anthropic}]
    config: {route: {paths: [/ai]}}
    targets:`
	for i, p := range providers {
		out += `
      - name: t` + string(rune('a'+i)) + `
        provider: ` + p + `
        config: {type: anthropic}`
	}
	return out
}

func pluginsNamed(doc *kong.Document, name string) []kong.Plugin {
	var out []kong.Plugin
	for _, p := range doc.Plugins {
		if p.Name == name {
			out = append(out, p)
		}
	}
	return out
}

func TestProviderTokenVaultScopedLikeProxy(t *testing.T) {
	src := providerTokenVaultDatastores + "models:" +
		providerTokenVaultModel("claude", "vault", "vault") +
		"\nmodel_providers:" + vaultProvider + "\n"
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)

	exchanges := pluginsNamed(result.Output, tokenVaultExchangePlugin)
	proxies := pluginsNamed(result.Output, "ai-proxy-advanced")
	require.Len(t, exchanges, 1)
	require.Len(t, proxies, 1)
	require.Equal(t, proxies[0].Route, exchanges[0].Route)
	require.Equal(t, proxies[0].Model, exchanges[0].Model)
	require.Equal(t, map[string]any{
		"provider":  "anthropic",
		"directory": "d",
		"storage": map[string]any{
			"backend":         "konnect",
			"current_version": "v1",
			"keys":            []any{map[string]any{"version": "v1", "key": "k"}},
		},
	}, exchanges[0].Config)
	require.Nil(t, proxies[0].Config["targets"].([]map[string]any)[0]["auth"])
	require.Len(t, result.Output.Routes, 1)
}

func TestProviderTokenVaultRejects(t *testing.T) {
	cases := []struct {
		name      string
		providers string
		targets   []string
		caps      string
		code      ConversionIssueCode
		reason    string
	}{
		{
			name:      "targets disagree",
			providers: vaultProvider + staticProvider,
			targets:   []string{"vault", "static"},
			code:      ConfigFieldInvalid,
			reason:    "every target must use the same provider token_vault",
		},
		{
			name: "static auth and token_vault",
			providers: vaultProvider + `
    config:
      auth:
        type: basic
        headers: [{name: x-api-key, value: secret}]`,
			code:   ConfigFieldInvalid,
			reason: "token_vault and config.auth are mutually exclusive",
		},
		{
			name: "no datastore",
			providers: `
  - name: vault
    type: anthropic
    token_vault: {provider: anthropic}`,
			code:   MissingRequiredConfigField,
			reason: `token_vault requires exactly one "token-vault" datastore`,
		},
		{
			name: "unknown datastore",
			providers: `
  - name: vault
    type: anthropic
    datastores: [{name: nope}]
    token_vault: {provider: anthropic}`,
			code:   UndefinedReference,
			reason: `references unknown datastore "nope"`,
		},
		{
			name: "wrong datastore type",
			providers: `
  - name: vault
    type: anthropic
    datastores: [{name: cache}]
    token_vault: {provider: anthropic}`,
			code:   ConfigFieldInvalid,
			reason: `token_vault does not support a "redis-ee" datastore`,
		},
		{
			name: "datastore without token_vault",
			providers: `
  - name: vault
    type: anthropic
    datastores: [{name: tv}]`,
			code:   ConfigFieldInvalid,
			reason: "datastores require token_vault",
		},
		{
			name: "no provider",
			providers: `
  - name: vault
    type: anthropic
    datastores: [{name: tv}]
    token_vault: {subject_token_type: api_key}`,
			code:   MissingRequiredConfigField,
			reason: "token_vault requires provider",
		},
		{
			name: "inline redis",
			providers: `
  - name: vault
    type: anthropic
    datastores: [{name: tv}]
    token_vault:
      provider: anthropic
      encryption_secrets: [s]
      redis: {host: redis.internal}`,
			code:   ConfigFieldInvalid,
			reason: "token_vault does not support redis or encryption_secrets",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targets := tc.targets
			if targets == nil {
				targets = []string{"vault"}
			}
			src := providerTokenVaultDatastores + "models:" +
				providerTokenVaultModel("claude", targets...) +
				"\nmodel_providers:" + tc.providers + "\n"
			result, err := Convert([]byte(src), testOptions)
			require.NoError(t, err)
			field := fmt.Sprintf("targets[%d].provider", len(targets)-1)
			issue := requireIssue(t, result.Issues, Error, tc.code, kindModel, "claude", field)
			require.Contains(t, issue.Reason, tc.reason)
			require.Empty(t, pluginsNamed(result.Output, "ai-proxy-advanced"))
			require.Empty(t, pluginsNamed(result.Output, tokenVaultExchangePlugin))
			require.Empty(t, result.Output.Routes)
		})
	}
}

func TestProviderTokenVaultRejectsWebSocket(t *testing.T) {
	src := providerTokenVaultDatastores + `
models:
  - name: rt
    type: model
    capabilities: [realtime]
    formats: [{type: openai}]
    config: {route: {paths: [/ai]}}
    targets:
      - name: gpt-realtime
        provider: vault
        config: {type: openai}
model_providers:
  - name: vault
    type: openai
    datastores: [{name: tv}]
    token_vault: {provider: openai}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "rt", "capabilities[0]")
	require.Contains(t, issue.Reason, "does not support the ws and wss protocols")
	require.Empty(t, pluginsNamed(result.Output, tokenVaultExchangePlugin))
}

// Two api models on one route share a route-scoped ai-proxy-advanced, so they
// must agree on the token vault.
func TestProviderTokenVaultSharedAPIRoute(t *testing.T) {
	api := func(name, provider string) string {
		return `
  - name: ` + name + `
    type: api
    capabilities: [files]
    formats: [{type: openai}]
    config: {route: {paths: [/ai]}}
    targets:
      - name: ` + name + `
        provider: ` + provider + `
        config: {type: openai}`
	}
	src := providerTokenVaultDatastores + "models:" + api("files-a", "vault") + api("files-b", "plain") + `
model_providers:
  - name: vault
    type: openai
    datastores: [{name: tv}]
    token_vault: {provider: openai}
  - name: plain
    type: openai
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "files-b", "targets")
	require.Contains(t, issue.Reason, "different provider token_vault")
	exchanges := pluginsNamed(result.Output, tokenVaultExchangePlugin)
	require.Len(t, exchanges, 1)
	require.Nil(t, exchanges[0].Model)
}

// One gateway serves one directory, so a second token-vault datastore fails
// the model that references it.
func TestProviderTokenVaultSecondDirectory(t *testing.T) {
	src := providerTokenVaultDatastores + "models:" +
		providerTokenVaultModel("claude", "vault") +
		providerTokenVaultModel("claude-other", "vault-other") + `
model_providers:` + vaultProvider + `
  - name: vault-other
    type: anthropic
    datastores: [{name: tv-other}]
    token_vault: {provider: anthropic}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "claude-other", "targets[0].provider")
	require.Contains(t, issue.Reason, "another entity already uses a different token-vault datastore")
	require.Len(t, pluginsNamed(result.Output, tokenVaultExchangePlugin), 1)
	require.Len(t, result.Output.Routes, 1)
}

func TestProviderTokenVaultBelowGate(t *testing.T) {
	src := providerTokenVaultDatastores + "models:" +
		providerTokenVaultModel("claude", "vault") +
		"\nmodel_providers:" + vaultProvider + "\n"
	result, err := Convert([]byte(src), Options{MinRuntimeVersion: "2.2"})
	require.NoError(t, err)
	requireIssue(t, result.Issues, Error, UnsupportedInMinRuntimeVersion, kindModel, "claude", "targets[0].provider")
	require.Empty(t, pluginsNamed(result.Output, tokenVaultExchangePlugin))
}

// Dropping a conversion-only MCP server prunes the callback route only when no
// entity uses a Token Vault.
func TestProviderTokenVaultKeepsCallbackRouteWhenMCPPruned(t *testing.T) {
	src := providerTokenVaultDatastores + "models:" +
		providerTokenVaultModel("claude", "vault") + `
model_providers:` + vaultProvider + `
mcp_servers:
  - type: conversion-only
    name: orphan
    config:
      url: https://orphan.internal
    tools:
      - {name: report, description: Get a report}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	requireIssue(t, result.Issues, Warning, ConfigFieldDropped, kindMCPServer, "orphan")
	require.Len(t, pluginsNamed(result.Output, tokenVaultExchangePlugin), 1)
	require.Len(t, result.Output.Routes, 1)
}
