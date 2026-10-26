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
	requireCallbackRoutes(t, result.Output.Routes)
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
			reason: "token_vault and credentials in config.auth are mutually exclusive",
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

// The validation pass checks every provider, also one that no model targets.
func TestProviderTokenVaultUnusedProvider(t *testing.T) {
	const models = `
models:
  - name: claude
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config: {route: {paths: [/ai]}}
    targets:
      - name: gpt-5.2
        provider: plain
        config: {type: openai}
`
	t.Run("static auth and token_vault fails", func(t *testing.T) {
		src := providerTokenVaultDatastores + models + `
model_providers:
  - name: plain
    type: openai
  - name: unused-vault
    type: openai
    datastores: [{name: tv}]
    token_vault: {provider: openai}
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: secret}]
`
		result, err := Convert([]byte(src), testOptions)
		require.NoError(t, err)
		issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid,
			kindModelProvider, "unused-vault", "token_vault")
		require.Contains(t, issue.Reason, "token_vault and credentials in config.auth are mutually exclusive")
		require.Empty(t, result.Output.Routes, "an unused provider adds no callback route")
		require.Empty(t, pluginsNamed(result.Output, tokenVaultExchangePlugin))
	})

	t.Run("auth field that resolveAuth ignores is accepted", func(t *testing.T) {
		src := providerTokenVaultDatastores + models + `
model_providers:
  - name: plain
    type: openai
  - name: unused-vault
    type: bedrock
    datastores: [{name: tv}]
    token_vault: {provider: bedrock}
    config:
      auth:
        assume_role_arn: arn:aws:iam::000000000000:role/r
        role_session_name: session
`
		result, err := Convert([]byte(src), testOptions)
		require.NoError(t, err)
		require.Empty(t, result.Issues)
		require.Empty(t, result.Output.Routes)
	})

	t.Run("provider field names the failed check", func(t *testing.T) {
		cases := []struct {
			name      string
			providers string
			field     string
			code      ConversionIssueCode
			reason    string
		}{
			{
				name: "no provider",
				providers: `
  - name: unused-vault
    type: openai
    datastores: [{name: tv}]
    token_vault: {subject_token_type: api_key}`,
				field:  "token_vault",
				code:   MissingRequiredConfigField,
				reason: "token_vault requires provider",
			},
			{
				name: "no datastore",
				providers: `
  - name: unused-vault
    type: openai
    token_vault: {provider: openai}`,
				field:  "datastores",
				code:   MissingRequiredConfigField,
				reason: `token_vault requires exactly one "token-vault" datastore`,
			},
			{
				name: "unknown datastore",
				providers: `
  - name: unused-vault
    type: openai
    datastores: [{name: nope}]
    token_vault: {provider: openai}`,
				field:  "datastores[0].name",
				code:   UndefinedReference,
				reason: `references unknown datastore "nope"`,
			},
			{
				name: "wrong datastore type",
				providers: `
  - name: unused-vault
    type: openai
    datastores: [{name: cache}]
    token_vault: {provider: openai}`,
				field:  "datastores[0].name",
				code:   ConfigFieldInvalid,
				reason: `token_vault does not support a "redis-ee" datastore`,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				src := providerTokenVaultDatastores + models +
					"model_providers:\n  - name: plain\n    type: openai" +
					tc.providers + "\n"
				result, err := Convert([]byte(src), testOptions)
				require.NoError(t, err)
				issue := requireIssue(t, result.Issues, Error, tc.code,
					kindModelProvider, "unused-vault", tc.field)
				require.Contains(t, issue.Reason, tc.reason)
				require.Empty(t, result.Output.Routes)
				require.Empty(t, pluginsNamed(result.Output, tokenVaultExchangePlugin))
			})
		}
	})
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
	requireCallbackRoutes(t, result.Output.Routes)
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
	requireCallbackRoutes(t, result.Output.Routes)
}

// The video lifecycle route has its own ai-proxy-advanced. It needs the same
// token-vault-exchange as the video route.
func TestProviderTokenVaultVideoLifecycle(t *testing.T) {
	src := providerTokenVaultDatastores + `
models:
  - name: sora
    type: model
    capabilities: [video]
    formats: [{type: openai}]
    config: {route: {paths: [/ai]}}
    targets:
      - name: sora-2
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
	proxies := pluginsNamed(result.Output, "ai-proxy-advanced")
	exchanges := pluginsNamed(result.Output, tokenVaultExchangePlugin)
	require.Len(t, exchanges, len(proxies))
	for i := range proxies {
		require.Equal(t, proxies[i].Route, exchanges[i].Route)
		require.Equal(t, proxies[i].Model, exchanges[i].Model)
	}
	require.Contains(t, string(*exchanges[len(exchanges)-1].Route), "openai-videos-lifecycle")
}

func TestProviderTokenVaultRejectsEmbeddingsProvider(t *testing.T) {
	src := providerTokenVaultDatastores + `
models:
  - name: semantic
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config:
      route: {paths: [/ai]}
      balancer:
        algorithm: semantic
        embeddings:
          provider: vault
          model:
            name: text-embedding-3-small
            config: {type: openai}
    targets:
      - name: gpt
        provider: plain
        config: {type: openai}
model_providers:
  - name: vault
    type: openai
    datastores: [{name: tv}]
    token_vault: {provider: openai}
  - name: plain
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: secret}]
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "semantic",
		"config.balancer.embeddings.provider")
	require.Contains(t, issue.Reason, "cannot set the credential of the embeddings request")
	require.Empty(t, pluginsNamed(result.Output, "ai-proxy-advanced"))
}

// An MCP server and a model provider with the same datastore share one
// callback route without an issue.
func TestProviderTokenVaultSharesCallbackRouteWithMCP(t *testing.T) {
	src := providerTokenVaultDatastores + "models:" +
		providerTokenVaultModel("claude", "vault") + `
model_providers:` + vaultProvider + `
mcp_servers:
  - type: passthrough-listener
    name: gh
    upstream_url: https://mcp.internal
    config:
      route: {paths: [/mcp/gh]}
    datastores: [{name: tv}]
    token_vault: {provider: github}
    tools:
      - {name: report, description: Get a report}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	requireCallbackRoutes(t, result.Output.Routes)
	require.Len(t, pluginsNamed(result.Output, tokenVaultExchangePlugin), 1)
}

// The datastore supplies the directory, so an inline token_vault.directory is
// ignored with a warning.
func TestProviderTokenVaultDatastoreOverridesInlineDirectory(t *testing.T) {
	src := providerTokenVaultDatastores + "models:" +
		providerTokenVaultModel("claude", "vault") + `
model_providers:
  - name: vault
    type: anthropic
    datastores: [{name: tv}]
    token_vault: {provider: anthropic, directory: inline}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Warning, ConfigFieldIgnored,
		kindModelProvider, "vault", "token_vault.directory")
	require.Contains(t, issue.Reason, `"tv"`)
	exchanges := pluginsNamed(result.Output, tokenVaultExchangePlugin)
	require.Len(t, exchanges, 1)
	require.Equal(t, "d", exchanges[0].Config["directory"])
}

func TestProviderTokenVaultKeepsInlineDirectoryWithoutDatastoreDirectory(t *testing.T) {
	src := providerTokenVaultDatastores + `
  - name: tv-nodir
    type: token-vault
    config: {current_version: v1, keys: [{version: v1, key: k}]}
models:` +
		providerTokenVaultModel("claude", "vault") + `
model_providers:
  - name: vault
    type: anthropic
    datastores: [{name: tv-nodir}]
    token_vault: {provider: anthropic, directory: inline}
`
	result, err := Convert([]byte(src), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	exchanges := pluginsNamed(result.Output, tokenVaultExchangePlugin)
	require.Len(t, exchanges, 1)
	require.Equal(t, "inline", exchanges[0].Config["directory"])
}
