package convert

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

func TestAuthStrategyKeyAuthForcesEmptyIdentityRealms(t *testing.T) {
	idp := &aigw.AuthStrategy{
		Name:        "agent-key-auth",
		DisplayName: "agent-key-auth",
		Type:        "key-auth",
		Config: map[string]any{
			"hide_credentials": true,
			"key_in_body":      false,
			"key_in_header":    true,
			"key_in_query":     false,
			"key_names":        []any{"x-api-key"},
			"principals":       map[string]any{"directory": "default", "enabled": true},
		},
	}

	p := authStrategyPlugin(idp)

	require.Equal(t, "key-auth", p.Name)
	require.Contains(t, p.Config, "identity_realms")
	require.Equal(t, []any{}, p.Config["identity_realms"], "identity_realms must be an empty array, not unset")
	require.Equal(t, anonymousConsumerName, p.Config["anonymous"])
	// The rest of the config passes through verbatim.
	require.Equal(t, true, p.Config["hide_credentials"])
	require.Equal(t, []any{"x-api-key"}, p.Config["key_names"])
	require.Equal(t, map[string]any{"directory": "default", "enabled": true}, p.Config["principals"])
}

func TestAuthStrategyKeyAuthLeavesIdentityRealmsUnsetWhenPrincipalsDisabled(t *testing.T) {
	cases := map[string]map[string]any{
		"principals disabled": {"principals": map[string]any{"enabled": false}},
		"no principals":       {"key_names": []any{"x-api-key"}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			p := authStrategyPlugin(&aigw.AuthStrategy{Type: "key-auth", Config: cfg})
			require.NotContains(t, p.Config, "identity_realms")
		})
	}
}

func TestEnsureAnonymousConsumerCreatesWhenMissing(t *testing.T) {
	c := newConverter(nil, Options{})

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 1, "expected anonymous consumer to be created")
	anon := c.out.Consumers[0]
	require.Equal(t, anonymousConsumerName, anon.Username)
	require.Equal(t, anonymousConsumerName, anon.CustomID)
	require.Len(t, anon.Plugins, 1, "expected request-termination plugin to be attached")
	require.Equal(t, "request-termination", anon.Plugins[0].Name)
	require.Equal(t, map[string]any{
		"status_code": unauthorizedStatusCode,
		"message":     unauthorizedErrorMessage,
	}, anon.Plugins[0].Config)
}

func TestEnsureAnonymousConsumerAttachesPluginWhenMissing(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{{
		Username: anonymousConsumerName,
		CustomID: anonymousConsumerName,
		Plugins: []kong.Plugin{{
			Name:   "rate-limiting",
			Config: map[string]any{"minute": 5},
		}},
	}}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 1, "should not duplicate the anonymous consumer")
	anon := c.out.Consumers[0]
	require.Len(t, anon.Plugins, 2, "expected request-termination plugin to be appended")
	require.Equal(t, "rate-limiting", anon.Plugins[0].Name, "existing plugin should be preserved")
	require.Equal(t, "request-termination", anon.Plugins[1].Name)
	require.Equal(t, map[string]any{
		"status_code": unauthorizedStatusCode,
		"message":     unauthorizedErrorMessage,
	}, anon.Plugins[1].Config)
}

func TestEnsureAnonymousConsumerHandlesSeparateUsernameAndCustomIDConsumers(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{
		{Username: anonymousConsumerName},
		{CustomID: anonymousConsumerName},
	}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 2, "should not create a new consumer when matches already exist")

	byUsername := c.out.Consumers[0]
	require.Len(t, byUsername.Plugins, 1, "consumer matched by username should get the plugin")
	require.Equal(t, "request-termination", byUsername.Plugins[0].Name)

	byCustomID := c.out.Consumers[1]
	require.Len(t, byCustomID.Plugins, 1, "consumer matched by custom_id should also get the plugin")
	require.Equal(t, "request-termination", byCustomID.Plugins[0].Name)
}

func TestEnsureAnonymousConsumerLeavesUnrelatedConsumersUntouched(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{
		{
			Username: "someone-else",
			CustomID: "someone-else",
			Plugins: []kong.Plugin{{
				Name:   "rate-limiting",
				Config: map[string]any{"minute": 5},
			}},
		},
	}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 2, "should append a new anonymous consumer alongside the unrelated one")

	unrelated := c.out.Consumers[0]
	require.Equal(t, "someone-else", unrelated.Username)
	require.Len(t, unrelated.Plugins, 1, "unrelated consumer's plugins should be untouched")
	require.Equal(t, "rate-limiting", unrelated.Plugins[0].Name)

	anon := c.out.Consumers[1]
	require.Equal(t, anonymousConsumerName, anon.Username)
	require.Equal(t, anonymousConsumerName, anon.CustomID)
	require.Len(t, anon.Plugins, 1, "expected request-termination plugin to be attached")
	require.Equal(t, "request-termination", anon.Plugins[0].Name)
}

func TestEnsureAnonymousConsumerOverwritesExistingPlugin(t *testing.T) {
	c := newConverter(nil, Options{})
	c.out.Consumers = []kong.Consumer{{
		Username: anonymousConsumerName,
		CustomID: anonymousConsumerName,
		Plugins: []kong.Plugin{{
			Name:   "request-termination",
			Config: map[string]any{"status_code": 403, "message": "custom"},
		}},
	}}

	c.ensureAnonymousConsumer()

	require.Len(t, c.out.Consumers, 1, "should not duplicate the anonymous consumer")
	anon := c.out.Consumers[0]
	require.Len(t, anon.Plugins, 1, "should not add a second request-termination plugin")
	require.Equal(t, map[string]any{
		"status_code": unauthorizedStatusCode,
		"message":     unauthorizedErrorMessage,
	}, anon.Plugins[0].Config, "existing plugin config should be forcefully overwritten")
	require.NotNil(t, anon.Plugins[0].Enabled)
	require.True(t, *anon.Plugins[0].Enabled, "overwritten plugin should always be enabled")
}

func authStrategyOverrideDoc(strategyConfig string) string {
	return `models:
  - name: a
    config: {route: {paths: [/a]}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
    access: {auth_strategies: [ka]}
  - name: b
    config: {route: {paths: [/b]}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
    access: {auth_strategies: [ka]}
model_providers:
  - name: openai-prod
    type: openai
    config:
      auth: {type: basic, headers: [{name: Authorization, value: x}]}
auth_strategies:
  - name: ka
    type: key-auth
    config: ` + strategyConfig + "\n"
}

func TestAuthStrategyWarnsOverrides(t *testing.T) {
	cases := map[string]struct {
		config string
		field  string
		want   []string
	}{
		"anonymous": {
			`{key_names: [k], anonymous: someone}`,
			"config.anonymous",
			[]string{
				`anonymous will be overridden to "anonymous"`,
				"On a model route with one auth strategy, anonymous will be removed",
			},
		},
		"identity_realms": {
			`{principals: {enabled: true}, identity_realms: [{scope: cp}]}`,
			"config.identity_realms",
			[]string{"identity_realms will be overridden to []"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := Convert([]byte(authStrategyOverrideDoc(tc.config)), testOptions)
			require.NoError(t, err)
			require.Len(t, result.Issues, 1, "two referencing models give one issue")
			issue := requireIssue(t, result.Issues, Warning, ConfigFieldOverridden, kindAuthStrategy, "ka", tc.field)
			for _, want := range tc.want {
				require.Contains(t, issue.Reason, want)
			}
		})
	}
}

func TestAuthStrategyDoesNotWarnMatchingValues(t *testing.T) {
	for _, cfg := range []string{
		`{key_names: [k], anonymous: anonymous}`,
		`{principals: {enabled: true}, identity_realms: []}`,
		`{principals: {enabled: false}, identity_realms: [{scope: cp}]}`,
	} {
		_, warnings, err := convertDoc([]byte(authStrategyOverrideDoc(cfg)), true)
		require.NoError(t, err, cfg)
		require.Empty(t, warnings, cfg)
	}
}

// A model that fails after it added the request-termination plugin to the
// user's anonymous consumer leaves that consumer as the user wrote it. The
// model fails on its realtime route, which allows only one auth strategy.
func TestFailedModelLeavesUserAnonymousConsumerUnchanged(t *testing.T) {
	for name, consumer := range map[string]string{
		"without plugins": `
  - name: anonymous`,
		"with its own request-termination": `
  - name: anonymous
    policies: [deny-403]`,
	} {
		t.Run(name, func(t *testing.T) {
			src := []byte(`
model_providers:
  - {name: openai-main, type: openai}
auth_strategies:
  - {name: keys-a, type: key-auth, config: {key_names: [a]}}
  - {name: keys-b, type: key-auth, config: {key_names: [b]}}
policies:
  - {name: deny-403, type: request-termination, config: {status_code: 403}}
consumers:` + consumer + `
models:
  - name: broken
    capabilities: [generate, realtime]
    targets: [{name: gpt-4o, provider: openai-main, config: {type: openai}}]
    access: {auth_strategies: [keys-a, keys-b]}
    config: {route: {paths: [/ai]}}
`)
			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "broken", "access.auth_strategies")
			require.Empty(t, result.Output.AIModels, "the model is left out")

			withoutModels, err := Convert([]byte(strings.Split(string(src), "models:\n")[0]), testOptions)
			require.NoError(t, err)
			require.Equal(t, withoutModels.Output.Consumers, result.Output.Consumers,
				"the anonymous consumer keeps the plugins the user wrote")
		})
	}
}

func TestAuthStrategyTokenVault(t *testing.T) {
	const doc = `
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
auth_strategies:
  - name: sso
    type: %s
    datastores: %s
    config: {issuer: https://idp.example.com}
    token_vault: {enable_credential_manager: true}
mcp_servers:
  - type: passthrough-listener
    name: m
    upstream_url: https://mcp.internal
    config: {route: {paths: [/mcp/m]}}
    access: {auth_strategies: [sso]}
    tools:
      - {name: report, description: Get a report}
`
	run := func(typ, datastores string) (*ConversionResult, error) {
		res, err := Convert([]byte(fmt.Sprintf(doc, typ, datastores)), testOptions)
		return res, err
	}
	t.Run("openid-connect adds the callback route once", func(t *testing.T) {
		res, err := run("openid-connect", "[{name: tv}]")
		require.NoError(t, err)
		require.Empty(t, res.Issues)
		require.Len(t, res.Output.Routes, 2)
		require.Equal(t, aimap.TokenVaultCallbackName, res.Output.Routes[0].Name)
		require.Equal(t, aimap.TokenVaultCredentialManagerName+"-sso", res.Output.Routes[1].Name)
	})
	t.Run("other type fails", func(t *testing.T) {
		res, _ := run("key-auth", "[{name: tv}]")
		requireIssue(t, res.Issues, Error, ConfigFieldInvalid, kindAuthStrategy, "sso", "token_vault")
		require.Empty(t, res.Output.Routes)
		require.Empty(t, res.Output.Services)
	})
	t.Run("missing datastore fails", func(t *testing.T) {
		res, _ := run("openid-connect", "[]")
		requireIssue(t, res.Issues, Error, MissingRequiredConfigField, kindAuthStrategy, "sso", "datastores")
		require.Empty(t, res.Output.Services)
	})
	t.Run("unknown datastore fails", func(t *testing.T) {
		res, _ := run("openid-connect", "[{name: nope}]")
		requireIssue(t, res.Issues, Error, UndefinedReference, kindAuthStrategy, "sso", "datastores[0].name")
		require.Empty(t, res.Output.Services)
	})
}

func TestAuthStrategyTokenVaultUnusedStrategyStillFails(t *testing.T) {
	res, _ := Convert([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
auth_strategies:
  - name: ka
    type: key-auth
    datastores: [{name: tv}]
    token_vault: {enable_credential_manager: true}
  - name: oidc
    type: openid-connect
    datastores: [{name: tv}]
    token_vault: {enable_credential_manager: true}
`), testOptions)
	requireIssue(t, res.Issues, Error, ConfigFieldInvalid, kindAuthStrategy, "ka", "token_vault")
	require.Len(t, res.Issues, 1)
	require.Empty(t, res.Output.Routes, "an unused strategy adds no callback route")
}

//nolint:gosec
const tokenVaultStrategyDoc = `
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
auth_strategies:
  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
    config:
      issuer: https://idp.example.com
%s
    token_vault: {enable_credential_manager: %t}
mcp_servers:
  - type: passthrough-listener
    name: a
    upstream_url: https://a.internal
    config: {route: {paths: [/mcp/a]}}
    access: {auth_strategies: [sso]}
    tools:
      - {name: report, description: Get a report}
  - type: passthrough-listener
    name: b
    upstream_url: https://b.internal
    config: {route: {paths: [/mcp/b]}}
    access: {auth_strategies: [sso]}
    tools:
      - {name: report, description: Get a report}
`

// tokenVaultOIDC holds the openid-connect configs of a conversion by route.
type tokenVaultOIDC struct {
	entity   []map[string]any
	callback map[string]any
	manager  map[string]any
}

func tokenVaultOIDCPlugins(res *ConversionResult) tokenVaultOIDC {
	var out tokenVaultOIDC
	for _, svc := range res.Output.Services {
		for _, route := range svc.Routes {
			for _, p := range route.Plugins {
				if p.Name == "openid-connect" {
					out.entity = append(out.entity, p.Config)
				}
			}
		}
	}
	for _, route := range res.Output.Routes {
		for _, p := range route.Plugins {
			if p.Name != "openid-connect" {
				continue
			}
			if route.Name == aimap.TokenVaultCallbackName {
				out.callback = p.Config
			} else {
				out.manager = p.Config
			}
		}
	}
	return out
}

func convertTokenVaultStrategy(t *testing.T, extra string, enabled bool) tokenVaultOIDC {
	t.Helper()
	res, err := Convert([]byte(fmt.Sprintf(tokenVaultStrategyDoc, extra, enabled)), testOptions)
	require.NoError(t, err)
	require.Empty(t, res.Issues)
	return tokenVaultOIDCPlugins(res)
}

func stubSessionSecret(t *testing.T, fn func() (string, error)) {
	t.Helper()
	prev := newSessionSecret
	t.Cleanup(func() { newSessionSecret = prev })
	newSessionSecret = fn
}

func TestAuthStrategyTokenVaultLeavesEntityPluginUnchanged(t *testing.T) {
	got := convertTokenVaultStrategy(t, "      hide_credentials: true", true)

	require.Len(t, got.entity, 2)
	for _, cfg := range got.entity {
		require.Equal(t, map[string]any{"issuer": "https://idp.example.com", "hide_credentials": true}, cfg)
	}
}

func TestAuthStrategyTokenVaultDisabledAddsNoRoute(t *testing.T) {
	got := convertTokenVaultStrategy(t, "", false)

	require.Nil(t, got.callback, "no Token Vault use adds no callback route")
	require.Nil(t, got.manager)
	for _, cfg := range got.entity {
		require.Equal(t, map[string]any{"issuer": "https://idp.example.com"}, cfg)
	}
}

func TestAuthStrategyTokenVaultCredentialManagerDefaults(t *testing.T) {
	calls := 0
	stubSessionSecret(t, func() (string, error) {
		calls++
		return "generated", nil
	})

	got := convertTokenVaultStrategy(t, "", true)

	require.Equal(t, "authorization:bearer", got.manager["upstream_access_token_header"])
	require.Equal(t, []any{"/vuc/sso/oidc/callback"}, got.manager["redirect_uri"])
	require.Equal(t, false, got.manager["hide_credentials"])
	require.Equal(t, "generated", got.manager["session_secret"])
	require.Equal(t, "generated", got.callback["session_secret"])
	require.Equal(t, 1, calls, "the secret is generated once per strategy")
}

func TestAuthStrategyTokenVaultCredentialManagerKeepsConfiguredValues(t *testing.T) {
	got := convertTokenVaultStrategy(t, `      upstream_access_token_header: x-token
      redirect_uri: [/custom]
      session_secret: mine`, true)

	require.Equal(t, "x-token", got.manager["upstream_access_token_header"])
	require.Equal(t, []any{"/custom"}, got.manager["redirect_uri"])
	require.Equal(t, "mine", got.manager["session_secret"])
	require.Equal(t, "mine", got.callback["session_secret"])
}

func TestAuthStrategyTokenVaultCallbackConfig(t *testing.T) {
	got := convertTokenVaultStrategy(t, `      hide_credentials: true
      preserve_query_args: false
      redirect_uri: [/custom]
      authorization_cookie_path: /
      session_cookie_path: /other
      session_cookie_same_site: Strict
      login_action: redirect
      upstream_access_token_header: x-token
      auth_methods: [bearer, session]`, true)

	require.Equal(t, map[string]any{
		"issuer":                       "https://idp.example.com",
		"hide_credentials":             false,
		"preserve_query_args":          true,
		"authorization_cookie_path":    "/_vault",
		"upstream_access_token_header": "authorization:bearer",
		"auth_methods":                 []any{"bearer", "session", "authorization_code"},
		"session_secret":               got.callback["session_secret"],
	}, got.callback)
	require.NotEmpty(t, got.callback["session_secret"])
}

func TestAuthStrategyTokenVaultCallbackAuthMethods(t *testing.T) {
	t.Run("unset stays unset", func(t *testing.T) {
		got := convertTokenVaultStrategy(t, "", true)
		require.NotContains(t, got.callback, "auth_methods")
	})
	t.Run("complete list stays", func(t *testing.T) {
		got := convertTokenVaultStrategy(t, "      auth_methods: [authorization_code, bearer, session]", true)
		require.Equal(t, []any{"authorization_code", "bearer", "session"}, got.callback["auth_methods"])
	})
}

func TestAuthStrategyTokenVaultSessionSecretFailure(t *testing.T) {
	stubSessionSecret(t, func() (string, error) { return "", errors.New("no entropy") })

	res, _ := Convert([]byte(fmt.Sprintf(tokenVaultStrategyDoc, "", true)), testOptions)

	requireIssue(t, res.Issues, Error, InternalError, kindAuthStrategy, "sso", "config.session_secret")
	require.Empty(t, res.Output.Services)
}

func TestGenerateSessionSecret(t *testing.T) {
	a, err := generateSessionSecret()
	require.NoError(t, err)
	b, err := generateSessionSecret()
	require.NoError(t, err)
	require.Regexp(t, `^[0-9a-f]{32}$`, a)
	require.NotEqual(t, a, b)
}

//nolint:gosec
const tokenVaultCallbackDoc = `
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
  - name: other
    type: token-vault
    config: {directory: o}
auth_strategies:
%s
mcp_servers:
  - type: passthrough-listener
    name: m
    upstream_url: https://mcp.internal
    config: {route: {paths: [/mcp/m]}}
    datastores: [{name: tv}]
    token_vault: {provider: github}
    access: {auth_strategies: [%s]}
    tools:
      - {name: report, description: Get a report}
`

func convertTokenVaultCallback(t *testing.T, strategies, refs string) *ConversionResult {
	t.Helper()
	res, err := Convert([]byte(fmt.Sprintf(tokenVaultCallbackDoc, strategies, refs)), testOptions)
	require.NoError(t, err)
	return res
}

func TestAuthStrategyTokenVaultDatastoreProtectsCallbackRoute(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://idp.example.com}`, "sso")

	require.Empty(t, res.Issues)
	require.Len(t, res.Output.Routes, 1, "no credential manager route")
	got := tokenVaultOIDCPlugins(res)
	require.Equal(t, "/_vault", got.callback["authorization_cookie_path"])
	require.Equal(t, []map[string]any{{"issuer": "https://idp.example.com"}}, got.entity)
}

func TestAuthStrategyWithoutDatastoreLeavesCallbackRouteOpen(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    config: {issuer: https://idp.example.com}`, "sso")

	require.Empty(t, res.Issues)
	require.Len(t, res.Output.Routes, 1)
	require.Nil(t, tokenVaultOIDCPlugins(res).callback)
}

func TestAuthStrategyTokenVaultUnusedStrategyLeavesCallbackRouteOpen(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://idp.example.com}
  - name: ka
    type: key-auth`, "ka")

	require.Empty(t, res.Issues)
	require.Nil(t, tokenVaultOIDCPlugins(res).callback)
}

func TestAuthStrategyTokenVaultOtherDatastoreWarns(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: other}]
    config: {issuer: https://idp.example.com}`, "sso")

	requireIssue(t, res.Issues, Warning, ConfigFieldIgnored, kindAuthStrategy, "sso", "datastores[0].name")
	require.Len(t, res.Issues, 1)
	require.Nil(t, tokenVaultOIDCPlugins(res).callback)
}

func TestAuthStrategyTokenVaultFirstStrategyProtectsCallbackRoute(t *testing.T) {
	res, err := Convert([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
auth_strategies:
  - name: first
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://first.example.com}
  - name: second
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://second.example.com}
mcp_servers:
  - type: passthrough-listener
    name: a
    upstream_url: https://a.internal
    config: {route: {paths: [/mcp/a]}}
    datastores: [{name: tv}]
    token_vault: {provider: github}
    access: {auth_strategies: [first]}
    tools:
      - {name: report, description: Get a report}
  - type: passthrough-listener
    name: b
    upstream_url: https://b.internal
    config: {route: {paths: [/mcp/b]}}
    access: {auth_strategies: [second]}
    tools:
      - {name: report, description: Get a report}
`), testOptions)
	require.NoError(t, err)

	requireIssue(t, res.Issues, Warning, ConfigFieldIgnored, kindAuthStrategy, "second", "datastores")
	require.Len(t, res.Issues, 1)
	require.Equal(t, "https://first.example.com", tokenVaultOIDCPlugins(res).callback["issuer"])
}

func TestAuthStrategyTokenVaultNonTokenVaultDatastoreFails(t *testing.T) {
	res, _ := Convert([]byte(`
datastores:
  - name: redis
    type: redis-ee
    config: {host: localhost}
auth_strategies:
  - name: sso
    type: openid-connect
    datastores: [{name: redis}]
    config: {issuer: https://idp.example.com}
mcp_servers:
  - type: passthrough-listener
    name: m
    upstream_url: https://mcp.internal
    config: {route: {paths: [/mcp/m]}}
    access: {auth_strategies: [sso]}
    tools:
      - {name: report, description: Get a report}
`), testOptions)

	requireIssue(t, res.Issues, Error, ConfigFieldInvalid, kindAuthStrategy, "sso", "datastores[0].name")
	require.Empty(t, res.Output.Services)
}

func TestAuthStrategyTokenVaultCallbackSessionSecretFailureRemovesRoute(t *testing.T) {
	stubSessionSecret(t, func() (string, error) { return "", errors.New("no entropy") })

	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://idp.example.com}`, "sso")

	requireIssue(t, res.Issues, Error, InternalError, kindAuthStrategy, "sso", "config.session_secret")
	require.Empty(t, res.Output.Routes, "an unprotected callback route is not output")
}

func TestAuthStrategyTokenVaultRollbackForgetsStrategy(t *testing.T) {
	doc, err := aigw.Parse([]byte(`
datastores:
  - name: tv
    type: token-vault
    config: {directory: d}
auth_strategies:
  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
`))
	require.NoError(t, err)
	c := newConverter(doc, testOptions)
	c.buildRegistries()

	c.convertEntity(issueSource(kindMCPServer, "m"), func() error {
		require.NoError(t, c.applyAuthStrategyTokenVault(c.authStrategies["sso"]))
		require.Equal(t, []string{"sso"}, c.out.TokenVaultAuthStrategies)
		return errSkipEntity
	})

	require.Empty(t, c.out.TokenVaultAuthStrategies)
}
