package convert

import (
	"errors"
	"fmt"
	"slices"
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
	t.Run("openid-connect adds the callback routes once", func(t *testing.T) {
		res, err := run("openid-connect", "[{name: tv}]")
		require.NoError(t, err)
		require.Empty(t, res.Issues)
		require.Len(t, res.Output.Routes, 4)
		require.Equal(t, aimap.TokenVaultCallbackName, res.Output.Routes[0].Name)
		require.Equal(t, aimap.TokenVaultCallbackInitiateName, res.Output.Routes[1].Name)
		require.Equal(t, aimap.TokenVaultCredentialManagerName+"-sso", res.Output.Routes[2].Name)
		require.Equal(t, aimap.TokenVaultCredentialManagerName+"-sso-login", res.Output.Routes[3].Name)
		require.Len(t, res.Output.Consumers, 1)
		require.Equal(t, managerAnonymousConsumerName, res.Output.Consumers[0].Username)
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

// tvOIDCConfigs holds the openid-connect configs of a conversion by route.
type tvOIDCConfigs struct {
	entity   []map[string]any
	callback map[string]any
	initiate map[string]any
	page     map[string]any
	login    map[string]any
}

func tvOIDCPlugins(res *ConversionResult) tvOIDCConfigs {
	var out tvOIDCConfigs
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
			} else if route.Name == aimap.TokenVaultCallbackInitiateName {
				out.initiate = p.Config
			} else if strings.HasPrefix(route.Name, aimap.TokenVaultCredentialManagerName+"-") &&
				strings.HasSuffix(route.Name, "-login") {
				out.login = p.Config
			} else {
				out.page = p.Config
			}
		}
	}
	return out
}

func convertTokenVaultStrategy(t *testing.T, extra string, enabled bool) tvOIDCConfigs {
	t.Helper()
	return tvOIDCPlugins(convertTokenVaultStrategyResult(t, extra, enabled))
}

func convertTokenVaultStrategyResult(t *testing.T, extra string, enabled bool) *ConversionResult {
	t.Helper()
	res, err := Convert([]byte(fmt.Sprintf(tokenVaultStrategyDoc, extra, enabled)), testOptions)
	require.NoError(t, err)
	return res
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

// The credential manager routes need the datastore directory. A strategy
// without the credential manager puts no directory in plugin config.
func TestAuthStrategyTokenVaultDatastoreWithoutDirectory(t *testing.T) {
	doc := strings.Replace(tokenVaultStrategyDoc,
		"config: {directory: d}", "config: {current_version: v1}", 1)

	res, err := Convert([]byte(fmt.Sprintf(doc, "", true)), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, res.Issues, Error, ConfigFieldInvalid,
		kindAuthStrategy, "sso", "datastores[0].name")
	require.Contains(t, issue.Reason, "requires config.directory")
	require.Empty(t, res.Output.Routes)

	res, err = Convert([]byte(fmt.Sprintf(doc, "", false)), testOptions)
	require.NoError(t, err)
	require.Empty(t, res.Issues)
	require.Len(t, res.Output.Services, 2)
}

func TestAuthStrategyTokenVaultDisabledAddsNoRoute(t *testing.T) {
	got := convertTokenVaultStrategy(t, "", false)

	require.Nil(t, got.callback, "no Token Vault use adds no callback route")
	require.Nil(t, got.initiate)
	require.Nil(t, got.page)
	require.Nil(t, got.login)
	for _, cfg := range got.entity {
		require.Equal(t, map[string]any{"issuer": "https://idp.example.com"}, cfg)
	}
}

// The page route serves the plugin page for an anonymous caller. The
// anonymous consumer makes the failed session pass through, and session is
// the only method, so the route starts no authorization code flow.
func TestAuthStrategyTokenVaultCredentialManagerPageConfig(t *testing.T) {
	got := convertTokenVaultStrategy(t, "", true)

	require.Equal(t, map[string]any{
		"issuer":                       "https://idp.example.com",
		"hide_credentials":             false,
		"upstream_access_token_header": "authorization:bearer",
		"anonymous":                    managerAnonymousConsumerName,
		"logout_query_arg":             "logout",
		"auth_methods":                 []any{"session"},
		"token_endpoint_auth_method":   "none",
		"session_secret":               got.page["session_secret"],
	}, got.page)
	require.NotContains(t, got.page, "redirect_uri",
		"the login route owns the authorization code flow")
}

// The login route starts the browser login. Without a strategy redirect_uri
// the plugin derives the redirect URI from the request, the exchange
// completes on the route itself, and the default login action serves the
// page there. A public client always uses PKCE.
func TestAuthStrategyTokenVaultCredentialManagerLoginConfig(t *testing.T) {
	calls := 0
	stubSessionSecret(t, func() (string, error) {
		calls++
		return "generated", nil
	})

	got := convertTokenVaultStrategy(t, "", true)

	require.Equal(t, map[string]any{
		"issuer":                              "https://idp.example.com",
		"hide_credentials":                    false,
		"upstream_access_token_header":        "authorization:bearer",
		"auth_methods":                        []any{"session", "authorization_code"},
		"authorization_cookie_path":           "/vuc/sso",
		"token_endpoint_auth_method":          "none",
		"require_proof_key_for_code_exchange": true,
		"session_secret":                      got.login["session_secret"],
	}, got.login)
	require.NotContains(t, got.login, "login_action")
	require.NotContains(t, got.login, "login_redirect_uri")
	require.Nil(t, got.callback, "the /_vault route carries no openid-connect plugin")
	require.Equal(t, "generated", got.initiate["session_secret"])
	require.Equal(t, 1, calls, "the secret is generated once per strategy")
}

// A strategy redirect_uri sends the exchange to its own path, so the
// authorization cookie must reach that path. The login action covers a
// session-authenticated visit too, so it bounces a signed-in caller back to
// the page route instead of serving the page under /login.
func TestAuthStrategyTokenVaultCredentialManagerLoginRedirectURI(t *testing.T) {
	got := convertTokenVaultStrategy(t,
		"      redirect_uri: [https://app.example.com/signin]", true)

	require.Equal(t, "/signin", got.login["authorization_cookie_path"])
	require.Equal(t, "redirect", got.login["login_action"])
	require.Equal(t, []any{"https://app.example.com/vuc/sso"}, got.login["login_redirect_uri"])
	require.Equal(t, []any{"session", "authorization_code"}, got.login["login_methods"])
	require.Equal(t, []any{}, got.login["login_tokens"],
		"the redirect must not carry the signed id token")
	require.Equal(t, []any{"https://app.example.com/signin"}, got.login["redirect_uri"])
	require.NotContains(t, got.page, "redirect_uri")
}

// Every route of one strategy shares one session secret, so the browser
// session works on each of them.
func TestAuthStrategyTokenVaultCredentialManagerSessionSecretShared(t *testing.T) {
	res := convertTokenVaultStrategyResult(t, "      session_secret: mine", true)
	got := tvOIDCPlugins(res)

	require.Equal(t, "mine", got.page["session_secret"])
	require.Equal(t, "mine", got.login["session_secret"])
	require.Equal(t, "mine", got.initiate["session_secret"])
}

// A client secret means a confidential client. The plugin then picks the token
// endpoint authentication method from the discovery document.
func TestAuthStrategyTokenVaultConfidentialClientKeepsClientAuth(t *testing.T) {
	got := convertTokenVaultStrategy(t, "      client_secret: [s3cr3t]", true)

	require.NotContains(t, got.page, "token_endpoint_auth_method")
	require.NotContains(t, got.login, "token_endpoint_auth_method")
	require.NotContains(t, got.initiate, "token_endpoint_auth_method")
}

func TestAuthStrategyTokenVaultKeepsConfiguredTokenEndpointAuthMethod(t *testing.T) {
	got := convertTokenVaultStrategy(t, "      token_endpoint_auth_method: private_key_jwt", true)

	require.Equal(t, "private_key_jwt", got.initiate["token_endpoint_auth_method"])
}

// The Token Vault routes always override upstream_access_token_header. The
// callback handler reads the subject token from the Authorization header.
// openid-connect rejects a relative redirect_uri, so every Token Vault route
// leaves it out.
func TestAuthStrategyTokenVaultCredentialManagerOverridesHeader(t *testing.T) {
	res := convertTokenVaultStrategyResult(t, `      upstream_access_token_header: x-token
      redirect_uri: [/custom]
      session_secret: mine`, true)
	got := tvOIDCPlugins(res)

	require.Equal(t, "authorization:bearer", got.page["upstream_access_token_header"])
	require.Equal(t, "authorization:bearer", got.login["upstream_access_token_header"])
	require.Equal(t, "mine", got.page["session_secret"])
	require.Equal(t, "mine", got.initiate["session_secret"])
	require.NotContains(t, got.login, "redirect_uri")
	require.NotContains(t, got.page, "redirect_uri")
	require.NotContains(t, got.login, "login_redirect_uri")
	require.Equal(t, "/vuc/sso", got.login["authorization_cookie_path"],
		"a relative redirect_uri drives no derivation")
	require.NotContains(t, got.initiate, "redirect_uri",
		"the enrollment route path is the one redirect URI the authorization server knows")
	requireStrategyIssue(t, res.Issues, ConfigFieldIgnored, "config.redirect_uri")
	requireStrategyIssue(t, res.Issues, ConfigFieldOverridden, "config.upstream_access_token_header")
}

// The start route reports each strategy field that it removes or replaces.
// Only there does the browser login run, and a login_action other than
// redirect loses the session cookie mid-flow.
func TestAuthStrategyTokenVaultInitiateOverrides(t *testing.T) {
	res := convertTokenVaultStrategyResult(t, `      hide_credentials: true
      preserve_query_args: false
      redirect_uri: [https://app.example.com/callback]
      session_cookie_path: /other
      session_cookie_same_site: Strict
      login_action: upstream
      upstream_access_token_header: x-token
      auth_methods: [bearer, session]`, true)
	got := tvOIDCPlugins(res)

	require.Equal(t, false, got.initiate["hide_credentials"])
	require.Equal(t, true, got.initiate["preserve_query_args"])
	require.Equal(t, "redirect", got.initiate["login_action"])
	require.Equal(t, "authorization:bearer", got.initiate["upstream_access_token_header"])
	require.NotContains(t, got.initiate, "redirect_uri")
	require.NotContains(t, got.initiate, "session_cookie_path")
	require.NotContains(t, got.initiate, "session_cookie_same_site")
	requireStrategyIssue(t, res.Issues, ConfigFieldOverridden, "config.hide_credentials")
	requireStrategyIssue(t, res.Issues, ConfigFieldOverridden, "config.preserve_query_args")
	requireStrategyIssue(t, res.Issues, ConfigFieldOverridden, "config.login_action")
	requireStrategyIssue(t, res.Issues, ConfigFieldOverridden, "config.upstream_access_token_header")
	requireStrategyIssue(t, res.Issues, ConfigFieldIgnored, "config.redirect_uri")
	requireStrategyIssue(t, res.Issues, ConfigFieldIgnored, "config.session_cookie_path")
	requireStrategyIssue(t, res.Issues, ConfigFieldIgnored, "config.session_cookie_same_site")
}

// Only the enrollment start route runs its own authorization code flow, so
// only it carries the login and cookie settings of that flow. The route
// lists its methods, because the plugin default enables every method. The
// empty login_tokens keeps the signed id token out of the redirect URL.
func TestAuthStrategyTokenVaultInitiateConfig(t *testing.T) {
	got := convertTokenVaultStrategy(t, "      auth_methods: [bearer]", true)

	require.Equal(t, map[string]any{
		"issuer":                       "https://idp.example.com",
		"hide_credentials":             false,
		"preserve_query_args":          true,
		"authorization_cookie_path":    "/_vault/callback/initiate",
		"upstream_access_token_header": "authorization:bearer",
		"login_action":                 "redirect",
		"login_methods":                []any{"authorization_code"},
		"login_tokens":                 []any{},
		"token_endpoint_auth_method":   "none",
		"auth_methods":                 []any{"session", "authorization_code"},
		"session_secret":               got.initiate["session_secret"],
	}, got.initiate)
}

func TestAuthStrategyTokenVaultInitiateAuthMethods(t *testing.T) {
	t.Run("unset becomes the explicit login methods", func(t *testing.T) {
		got := convertTokenVaultStrategy(t, "", true)
		require.Equal(t, []any{"session", "authorization_code"}, got.initiate["auth_methods"])
	})
	t.Run("a different strategy list is replaced with a warning", func(t *testing.T) {
		res := convertTokenVaultStrategyResult(t, "      auth_methods: [bearer]", true)
		require.Equal(t, []any{"session", "authorization_code"},
			tvOIDCPlugins(res).initiate["auth_methods"])
		requireStrategyIssue(t, res.Issues, ConfigFieldOverridden, "config.auth_methods")
	})
	t.Run("the login list stays", func(t *testing.T) {
		got := convertTokenVaultStrategy(t,
			"      auth_methods: [session, authorization_code]", true)
		require.Equal(t, []any{"session", "authorization_code"}, got.initiate["auth_methods"])
		require.Equal(t, []any{"session"}, got.page["auth_methods"])
		require.Equal(t, []any{"session", "authorization_code"}, got.login["auth_methods"])
	})
}

func TestEnsureManagerAnonymousConsumer(t *testing.T) {
	c := newConverter(nil, Options{})

	c.ensureManagerAnonymousConsumer()
	c.ensureManagerAnonymousConsumer()
	require.Len(t, c.out.Consumers, 1)
	require.Equal(t, managerAnonymousConsumerName, c.out.Consumers[0].Username)
	require.Empty(t, c.out.Consumers[0].Plugins,
		"the manager page route must reach the credential manager")

	c.out.Consumers = append(c.out.Consumers, kong.Consumer{Username: "other"})
	c.ensureManagerAnonymousConsumer()
	require.Len(t, c.out.Consumers, 2, "an existing consumer is reused")
}

// requireStrategyIssue asserts that issues hold a warning with code for one
// auth strategy field.
func requireStrategyIssue(t *testing.T, issues []ConversionIssue, code ConversionIssueCode, field string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == code && issue.Source.Kind == kindAuthStrategy &&
			slices.Equal(issue.Source.Fields, []string{field}) {
			require.Equal(t, Warning, issue.Severity)
			return
		}
	}
	require.Failf(t, "issue not found", "no %s issue for %s in %v", code, field, issues)
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

// The /_vault route stays open, and only the enrollment start route runs the
// authorization code flow, so only it carries an authorization cookie.
func TestAuthStrategyTokenVaultDatastoreProtectsCallbackRoutes(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://idp.example.com}`, "sso")

	require.Empty(t, res.Issues)
	requireCallbackRoutes(t, res.Output.Routes,
		aimap.TokenVaultCallbackName, aimap.TokenVaultCallbackInitiateName)
	got := tvOIDCPlugins(res)
	require.Nil(t, got.callback, "the /_vault route carries no openid-connect plugin")
	require.Equal(t, "/_vault/callback/initiate", got.initiate["authorization_cookie_path"])
	require.Equal(t, []map[string]any{{"issuer": "https://idp.example.com"}}, got.entity)
}

func TestAuthStrategyWithoutDatastoreLeavesCallbackRouteOpen(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    config: {issuer: https://idp.example.com}`, "sso")

	require.Empty(t, res.Issues)
	requireCallbackRoutes(t, res.Output.Routes)
	require.Nil(t, tvOIDCPlugins(res).initiate,
		"without a matching strategy no start route exists")
}

func TestAuthStrategyTokenVaultUnusedStrategyLeavesCallbackRouteOpen(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: tv}]
    config: {issuer: https://idp.example.com}
  - name: ka
    type: key-auth`, "ka")

	require.Empty(t, res.Issues)
	requireCallbackRoutes(t, res.Output.Routes)
	require.Nil(t, tvOIDCPlugins(res).initiate,
		"an unused strategy adds no start route")
}

func TestAuthStrategyTokenVaultOtherDatastoreWarns(t *testing.T) {
	res := convertTokenVaultCallback(t, `  - name: sso
    type: openid-connect
    datastores: [{name: other}]
    config: {issuer: https://idp.example.com}`, "sso")

	requireIssue(t, res.Issues, Warning, ConfigFieldIgnored, kindAuthStrategy, "sso", "datastores[0].name")
	require.Len(t, res.Issues, 1)
	requireCallbackRoutes(t, res.Output.Routes)
	require.Nil(t, tvOIDCPlugins(res).initiate,
		"a strategy with another datastore adds no start route")
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
	require.Equal(t, "https://first.example.com", tvOIDCPlugins(res).initiate["issuer"])
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
