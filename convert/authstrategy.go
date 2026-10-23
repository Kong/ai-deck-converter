package convert

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

const authStrategyTypeOpenIDConnect = "openid-connect"

// enableCredentialManagerKey is the token_vault key of an auth strategy that
// turns on all Token Vault output.
const enableCredentialManagerKey = "enable_credential_manager" //nolint:gosec

// defaultUpstreamAccessTokenHeader is the openid-connect default of an enabled
// Token Vault strategy that sets no upstream_access_token_header.
const defaultUpstreamAccessTokenHeader = "authorization:bearer"

// anonymousConsumerName is the username/custom_id of the synthesized Consumer
// that auth-strategy authentication plugins fall back to when a request
// isn't authenticated, so it can be rejected by a request-termination plugin
// instead of silently reaching the upstream.
const (
	anonymousConsumerName    = "anonymous"
	unauthorizedStatusCode   = 401
	unauthorizedErrorMessage = "Unauthorized"
)

// scopedAuthStrategyPlugins builds one authentication plugin per auth strategy
// reference of the entity src, each configured to fall back to the anonymous
// consumer. An unknown reference fails the entity, so it is never left
// unprotected.
func (c *Converter) scopedAuthStrategyPlugins(src ConversionIssueSource, refs []string) ([]kong.Plugin, error) {
	var plugins []kong.Plugin
	seen := map[string]bool{}
	for i, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		idp := c.authStrategies[ref]
		if idp == nil {
			return nil, c.fail(src.at(fmt.Sprintf("access.auth_strategies[%d]", i)), UndefinedReference,
				"unknown auth strategy reference %q", ref)
		}
		if err := c.checkAuthStrategyType(src, fmt.Sprintf("access.auth_strategies[%d]", i), idp); err != nil {
			return nil, err
		}
		c.warnAuthStrategyOverrides(idp)
		if err := c.applyAuthStrategyTokenVault(idp); err != nil {
			return nil, err
		}
		plugins = append(plugins, authStrategyPlugin(idp))
	}
	return plugins, nil
}

func (c *Converter) checkAuthStrategyType(src ConversionIssueSource, field string, idp *aigw.AuthStrategy) error {
	if authPolicyTypes[idp.Type] {
		return nil
	}
	return c.fail(src.at(field), ConfigFieldInvalid,
		"auth strategy %q has type %q, but the type must be key-auth or openid-connect",
		idp.Name, idp.Type)
}

// validateAuthStrategyTokenVaults reports the token_vault errors of every auth
// strategy, also of a strategy that no entity references.
// The output does not keep the callback route of an unused strategy.
func (c *Converter) validateAuthStrategyTokenVaults() {
	for i := range c.src.AuthStrategies {
		snapshot := *c.out
		_ = c.applyAuthStrategyTokenVault(&c.src.AuthStrategies[i])
		*c.out = snapshot
	}
}

// applyAuthStrategyTokenVault records an openid-connect strategy with a
// token-vault Datastore for secureTokenVaultCallbackRoute. An enabled
// credential manager also adds the callback and credential manager routes.
// Any other strategy type with a token_vault fails.
func (c *Converter) applyAuthStrategyTokenVault(idp *aigw.AuthStrategy) error {
	src := issueSource(kindAuthStrategy, idp.Name)
	if idp.TokenVault != nil && idp.Type != authStrategyTypeOpenIDConnect {
		return c.fail(src.at("token_vault"), ConfigFieldInvalid,
			"auth strategy %q: token_vault requires type %q, not %q",
			idp.Name, authStrategyTypeOpenIDConnect, idp.Type)
	}
	if idp.Type != authStrategyTypeOpenIDConnect {
		return nil
	}
	enabled, _ := idp.TokenVault[enableCredentialManagerKey].(bool)
	if enabled && !c.supports(aimap.FeatureAuthStrategyTokenVault) {
		return c.failUnsupported(src.at("token_vault"), aimap.FeatureAuthStrategyTokenVault, "token_vault")
	}
	ds, err := c.authStrategyTokenVaultDatastore(src, idp, enabled)
	if err != nil || ds == nil {
		return err
	}
	if !slices.Contains(c.out.TokenVaultAuthStrategies, idp.Name) {
		c.out.TokenVaultAuthStrategies = append(c.out.TokenVaultAuthStrategies, idp.Name)
	}
	if !enabled {
		return nil
	}
	owner := fmt.Sprintf("auth strategy %q", idp.Name)
	routeSource := source(kindAuthStrategy, idp.Name, "datastores")
	if err := c.ensureTokenVaultCallbackRoute(src.at("datastores[0].name"), owner, ds, routeSource); err != nil {
		return err
	}
	return c.credentialManagerRoute(src, idp, ds)
}

// authStrategyTokenVaultDatastore returns the token-vault Datastore of the
// strategy, or nil if the strategy has none. The credential manager requires
// one.
func (c *Converter) authStrategyTokenVaultDatastore(
	src ConversionIssueSource, idp *aigw.AuthStrategy, required bool,
) (*aigw.Datastore, error) {
	if len(idp.Datastores) == 0 && !required {
		return nil, nil
	}
	if len(idp.Datastores) != 1 {
		return nil, c.fail(src.at("datastores"), MissingRequiredConfigField,
			"auth strategy %q: token_vault requires exactly one %q datastore",
			idp.Name, aimap.DatastoreTypeTokenVault)
	}
	ds := c.datastores[idp.Datastores[0].Name]
	if ds == nil {
		return nil, c.fail(src.at("datastores[0].name"), UndefinedReference,
			"auth strategy %q references unknown datastore %q", idp.Name, idp.Datastores[0].Name)
	}
	if ds.Type != aimap.DatastoreTypeTokenVault {
		return nil, c.fail(src.at("datastores[0].name"), ConfigFieldInvalid,
			"auth strategy %q does not support a %q datastore; use %q instead",
			idp.Name, ds.Type, aimap.DatastoreTypeTokenVault)
	}
	return ds, nil
}

// applyTokenVaultOIDCDefaults sets the openid-connect fields that the
// credential manager route needs and the strategy does not set.
func (c *Converter) applyTokenVaultOIDCDefaults(
	src ConversionIssueSource, idp *aigw.AuthStrategy, cfg map[string]any,
) error {
	if isEmptyConfigValue(cfg["upstream_access_token_header"]) {
		cfg["upstream_access_token_header"] = defaultUpstreamAccessTokenHeader
	}
	if _, ok := cfg["hide_credentials"]; !ok {
		cfg["hide_credentials"] = false
	}
	if isEmptyConfigValue(cfg["redirect_uri"]) {
		cfg["redirect_uri"] = []any{
			aimap.TokenVaultCredentialManagerPath + "/" + idp.Name + "/oidc/callback",
		}
	}
	return c.applySessionSecret(src, idp, cfg)
}

// applySessionSecret sets a generated session_secret when cfg has none. Every
// plugin of the strategy gets the same secret. The browser session then works
// on the credential manager route and on the callback route.
func (c *Converter) applySessionSecret(src ConversionIssueSource, idp *aigw.AuthStrategy, cfg map[string]any) error {
	if !isEmptyConfigValue(cfg["session_secret"]) {
		return nil
	}
	secret, ok := c.sessionSecrets[idp.Name]
	if !ok {
		var err error
		if secret, err = newSessionSecret(); err != nil {
			return c.fail(src.at("config.session_secret"), InternalError,
				"auth strategy %q: cannot generate session_secret: %v", idp.Name, err)
		}
		c.sessionSecrets[idp.Name] = secret
	}
	cfg["session_secret"] = secret
	return nil
}

// newSessionSecret makes the session secret of a strategy. Tests replace it.
var newSessionSecret = generateSessionSecret

func generateSessionSecret() (string, error) {
	bytes := make([]byte, 16) //nolint:mnd
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// callbackOIDCRemovedFields are the openid-connect fields that the callback
// route must have at their defaults.
var callbackOIDCRemovedFields = []string{
	"redirect_uri",
	"login_action",
	"session_cookie_same_site",
	"session_cookie_path",
}

// callbackOIDCAuthMethods are the openid-connect auth methods that the browser
// flow on the callback route needs.
var callbackOIDCAuthMethods = []string{"session", "authorization_code"}

// callbackOIDCConfig returns the openid-connect config of the Token Vault
// callback route. The callback handler reads the access token from the
// Authorization header. With no redirect_uri, the login returns to the
// requested callback URL.
func (c *Converter) callbackOIDCConfig(src ConversionIssueSource, idp *aigw.AuthStrategy) (map[string]any, error) {
	cfg := maps.Clone(idp.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	for _, field := range callbackOIDCRemovedFields {
		delete(cfg, field)
	}
	cfg["hide_credentials"] = false
	cfg["preserve_query_args"] = true
	cfg["authorization_cookie_path"] = aimap.TokenVaultCallbackPath
	cfg["upstream_access_token_header"] = defaultUpstreamAccessTokenHeader
	if methods := configStrings(cfg, "auth_methods"); len(methods) > 0 {
		out := make([]any, 0, len(methods)+len(callbackOIDCAuthMethods))
		for _, m := range methods {
			out = append(out, m)
		}
		for _, m := range callbackOIDCAuthMethods {
			if !slices.Contains(methods, m) {
				out = append(out, m)
			}
		}
		cfg["auth_methods"] = out
	}
	if err := c.applySessionSecret(src, idp, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// secureTokenVaultCallbackRoute adds an openid-connect plugin to the Token
// Vault callback route. The first used strategy with the Datastore of the
// route gives the config. The callback handler then binds each enrollment to
// the principal that starts it.
func (c *Converter) secureTokenVaultCallbackRoute() {
	i := slices.IndexFunc(c.out.Routes, func(r kong.Route) bool { return r.Name == aimap.TokenVaultCallbackName })
	if i < 0 {
		return
	}
	var chosen *aigw.AuthStrategy
	for _, name := range c.out.TokenVaultAuthStrategies {
		idp := c.authStrategies[name]
		src := issueSource(kindAuthStrategy, name)
		ds := c.datastores[idp.Datastores[0].Name]
		dsConfig, err := aimap.ApplyTokenVaultDatastore(nil, ds)
		if err != nil || !reflect.DeepEqual(c.out.Routes[i].Plugins[0].Config, dsConfig) {
			c.warn(src.at("datastores[0].name"), ConfigFieldIgnored,
				"auth strategy %q references datastore %q, but the Token Vault callback route "+
					"uses a different token-vault datastore. The strategy does not protect the callback route.",
				name, ds.Name)
			continue
		}
		if chosen != nil {
			c.warn(src.at("datastores"), ConfigFieldIgnored,
				"auth strategy %q does not protect the Token Vault callback route. "+
					"Auth strategy %q already protects it.",
				name, chosen.Name)
			continue
		}
		chosen = idp
	}
	if chosen == nil {
		return
	}
	src := issueSource(kindAuthStrategy, chosen.Name)
	cfg, err := c.callbackOIDCConfig(src, chosen)
	if err != nil {
		// An unprotected route lets a victim complete an enrollment of an attacker.
		c.out.Routes = slices.Delete(slices.Clone(c.out.Routes), i, i+1)
		return
	}
	routes := slices.Clone(c.out.Routes)
	routes[i].Plugins = append(slices.Clone(routes[i].Plugins), kong.Plugin{
		Name:   authStrategyTypeOpenIDConnect,
		Config: cfg,
		Source: source(kindAuthStrategy, chosen.Name, "datastores"),
	})
	c.out.Routes = routes
}

// revealTokenVaultCredentials sets hide_credentials to false on each
// openid-connect plugin of a route that uses a Token Vault. The Token Vault
// plugins read the subject token of the caller from the request.
func (c *Converter) revealTokenVaultCredentials() {
	routes := c.tokenVaultRouteNames()
	if len(routes) == 0 {
		return
	}

	plugins, changed := c.revealedTokenVaultPlugins(c.out.Plugins, func(p kong.Plugin) bool {
		return p.Route != nil && routes[string(*p.Route)]
	})
	if changed {
		c.out.Plugins = plugins
	}

	var services []kong.Service
	for i, service := range c.out.Services {
		clonedRoutes := false
		for j, route := range service.Routes {
			plugins, changed := c.revealedTokenVaultPlugins(route.Plugins, func(kong.Plugin) bool {
				return routes[route.Name]
			})
			if !changed {
				continue
			}
			if services == nil {
				services = slices.Clone(c.out.Services)
			}
			if !clonedRoutes {
				services[i].Routes = slices.Clone(service.Routes)
				clonedRoutes = true
			}
			services[i].Routes[j].Plugins = plugins
		}
	}
	if services != nil {
		c.out.Services = services
	}
}

// tokenVaultRouteNames returns the names of the routes with a Token Vault
// plugin.
func (c *Converter) tokenVaultRouteNames() map[string]bool {
	routes := map[string]bool{}
	for _, p := range c.out.Plugins {
		if p.Name == tokenVaultExchangePlugin && p.Route != nil {
			routes[string(*p.Route)] = true
		}
	}
	for _, service := range c.out.Services {
		for _, route := range service.Routes {
			for _, p := range route.Plugins {
				auth, _ := p.Config["auth"].(map[string]any)
				if p.Name == "ai-mcp-proxy" && auth["provider"] == aimap.UpstreamAuthProviderTokenVault {
					routes[route.Name] = true
				}
			}
		}
	}
	return routes
}

// revealedTokenVaultPlugins returns a copy of plugins in which each selected
// openid-connect plugin has hide_credentials set to false. It reports false
// when no plugin changes, and then returns plugins unchanged.
func (c *Converter) revealedTokenVaultPlugins(
	plugins []kong.Plugin, selected func(kong.Plugin) bool,
) ([]kong.Plugin, bool) {
	var out []kong.Plugin
	for i, p := range plugins {
		if p.Name != authStrategyTypeOpenIDConnect || !selected(p) {
			continue
		}
		if hide, _ := p.Config["hide_credentials"].(bool); !hide {
			continue
		}
		if p.Source != nil {
			c.warn(issueSource(kindAuthStrategy, p.Source.EntityName).at("config.hide_credentials"),
				ConfigFieldOverridden,
				"auth strategy %q sets config.hide_credentials to true. "+
					"On a route that uses a Token Vault, hide_credentials will be overridden to false, "+
					"because the Token Vault reads the subject token of the caller from the request.",
				p.Source.EntityName)
		}
		if out == nil {
			out = slices.Clone(plugins)
		}
		cfg := maps.Clone(p.Config)
		cfg["hide_credentials"] = false
		out[i].Config = cfg
	}
	if out == nil {
		return plugins, false
	}
	return out, true
}

// credentialManagerRoute adds the route of the auth strategy that serves the
// Token Vault credential manager. The route has its own openid-connect plugin
// with the config of the strategy.
func (c *Converter) credentialManagerRoute(
	src ConversionIssueSource, idp *aigw.AuthStrategy, ds *aigw.Datastore,
) error {
	name := aimap.TokenVaultCredentialManagerName + "-" + idp.Name
	for _, route := range c.out.Routes {
		if route.Name == name {
			return nil
		}
	}
	oidcConfig := maps.Clone(idp.Config)
	if oidcConfig == nil {
		oidcConfig = map[string]any{}
	}
	if err := c.applyTokenVaultOIDCDefaults(src, idp, oidcConfig); err != nil {
		return err
	}
	config, err := aimap.ApplyTokenVaultDatastore(nil, ds)
	if err != nil {
		return c.fail(src.at("datastores[0].name"), ConfigFieldInvalid, "auth strategy %q: %v", idp.Name, err)
	}
	routeSource := source(kindAuthStrategy, idp.Name, "token_vault")
	c.out.Routes = append(c.out.Routes, kong.Route{
		Name:      name,
		Paths:     []string{aimap.TokenVaultCredentialManagerPath + "/" + idp.Name},
		Protocols: []string{"http", "https"},
		Plugins: []kong.Plugin{
			{
				Name:   authStrategyTypeOpenIDConnect,
				Config: oidcConfig,
				Source: routeSource,
			},
			{
				Name:   aimap.TokenVaultCredentialManagerName,
				Config: config,
				Source: routeSource,
			},
		},
		Source: routeSource,
	})
	return nil
}

// warnAuthStrategyOverrides reports the auth strategy config values that
// authStrategyPlugin replaces.
func (c *Converter) warnAuthStrategyOverrides(idp *aigw.AuthStrategy) {
	src := issueSource(kindAuthStrategy, idp.Name)
	if v := idp.Config["anonymous"]; !isEmptyConfigValue(v) && v != "" && v != anonymousConsumerName {
		c.warn(src.at("config.anonymous"), ConfigFieldOverridden,
			"auth strategy %q sets config.anonymous to %v. This will be ignored. "+
				"Where an entity has more than one auth strategy, or on an agent or MCP server, "+
				"anonymous will be overridden to %q, which a request-termination plugin rejects. "+
				"On a model route with one auth strategy, anonymous will be removed, "+
				"so the auth plugin rejects unauthenticated requests itself.",
			idp.Name, v, anonymousConsumerName)
	}
	if idp.Type == "key-auth" && keyAuthPrincipalsEnabled(idp.Config) {
		if v, ok := idp.Config["identity_realms"]; ok && !isEmptyConfigValue(v) {
			c.warn(src.at("config.identity_realms"), ConfigFieldOverridden,
				"auth strategy %q enables config.principals "+
					"and sets config.identity_realms. "+
					"This will be ignored, and identity_realms will be overridden to [] "+
					"because the key-auth schema requires it when principals is enabled.",
				idp.Name)
		}
	}
}

// authStrategyPlugin builds a Kong authentication plugin from an AI
// Gateway auth strategy, with config.anonymous set so failed
// authentication falls back to the anonymous consumer instead of erroring.
func authStrategyPlugin(idp *aigw.AuthStrategy) kong.Plugin {
	cfg := make(map[string]any, len(idp.Config)+1)
	for k, v := range idp.Config {
		cfg[k] = v
	}
	cfg["anonymous"] = anonymousConsumerName
	if idp.Type == "key-auth" {
		forceKeyAuthIdentityRealms(cfg)
	}
	return kong.Plugin{
		Name:   idp.Type,
		Config: cfg,
		Source: source("identity_provider", idp.Name, "config"),
	}
}

// withoutAnonymousFallback returns a copy of p without config.anonymous.
// request-termination does not run on ws and wss routes.
// An anonymous fallback there lets unauthenticated clients through.
func withoutAnonymousFallback(p kong.Plugin) kong.Plugin {
	cfg := make(map[string]any, len(p.Config))
	maps.Copy(cfg, p.Config)
	delete(cfg, "anonymous")
	p.Config = cfg
	return p
}

// forceKeyAuthIdentityRealms enforces the key-auth schema constraint that when
// principal hydration is enabled (config.principals.enabled = true),
// config.identity_realms must be present as an empty array rather than unset,
// as identity_realms is set by the DP to the default realm.
// See https://github.com/Kong/kong-ee/blob/master/kong/plugins/key-auth/schema.lua#L63
func forceKeyAuthIdentityRealms(cfg map[string]any) {
	if keyAuthPrincipalsEnabled(cfg) {
		cfg["identity_realms"] = []any{}
	}
}

func keyAuthPrincipalsEnabled(cfg map[string]any) bool {
	principals, ok := cfg["principals"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := principals["enabled"].(bool)
	return enabled
}

// ensureAnonymousConsumer appends the anonymous Consumer (with a
// request-termination plugin so unauthenticated requests get a 401) if one
// isn't already present in the output document.
//
// It never changes a consumer in place. convertEntity and convertModels
// restore c.out from a shallow copy, so an in-place change would survive a
// rollback.
func (c *Converter) ensureAnonymousConsumer() {
	enabled := true
	requestTerminationPlugin := kong.Plugin{
		Name:    "request-termination",
		Enabled: &enabled,
		Config: map[string]any{
			"status_code": unauthorizedStatusCode,
			"message":     unauthorizedErrorMessage,
		},
	}

	// Overwrite for both 'username: anonymous' and 'custom_id: anonymous' because
	// our auth plugins look for both at random unpredictable loop iterations
	providedConfigContainsAnonConsumer := false
	var consumers []kong.Consumer
	for i := range c.out.Consumers {
		if c.out.Consumers[i].Username != anonymousConsumerName && c.out.Consumers[i].CustomID != anonymousConsumerName {
			continue
		}
		providedConfigContainsAnonConsumer = true
		plugins, changed := withPlugin(c.out.Consumers[i].Plugins, requestTerminationPlugin)
		if !changed {
			continue
		}
		if consumers == nil {
			consumers = slices.Clone(c.out.Consumers)
		}
		consumers[i].Plugins = plugins
	}
	if consumers != nil {
		c.out.Consumers = consumers
	}
	if providedConfigContainsAnonConsumer {
		return
	}

	c.out.Consumers = append(c.out.Consumers, kong.Consumer{
		Username: anonymousConsumerName,
		CustomID: anonymousConsumerName,
		Plugins:  []kong.Plugin{requestTerminationPlugin},
	})
}

// withPlugin returns a copy of plugins in which p replaces the first plugin of
// the same name, or p is appended. It reports false when plugins already has
// p, and then returns plugins unchanged.
func withPlugin(plugins []kong.Plugin, p kong.Plugin) ([]kong.Plugin, bool) {
	i := slices.IndexFunc(plugins, func(q kong.Plugin) bool { return q.Name == p.Name })
	if i >= 0 && reflect.DeepEqual(plugins[i], p) {
		return plugins, false
	}
	out := slices.Clone(plugins)
	if i >= 0 {
		out[i] = p
		return out, true
	}
	return append(out, p), true
}
