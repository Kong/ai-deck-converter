package convert

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"net/url"
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

// managerAnonymousConsumerName is the username/custom_id of the Consumer
// that the credential manager page route falls back to. It carries no
// request-termination, because the unauthenticated caller must reach the
// credential manager, which serves its login page.
const managerAnonymousConsumerName = aimap.TokenVaultCredentialManagerName + "-anonymous"

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
	c.tvOIDCSilent = true
	defer func() { c.tvOIDCSilent = false }()
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

// tvManagerOIDCConfig returns the openid-connect config of the Token Vault
// self-serve credential manager page route. The plugin serves a browser page
// and does no authentication of its own. Session is the only method, and the
// anonymous consumer makes a failed session pass through, so the credential
// manager serves its login page instead of a 401. The route starts no
// authorization code flow, so it carries no redirect_uri.
func (c *Converter) tvManagerOIDCConfig(
	src ConversionIssueSource, idp *aigw.AuthStrategy,
) (map[string]any, error) {
	b := c.tvOIDC(src, idp)
	b.remove(tvManagerOIDCRemovedFields...)
	delete(b.cfg, "redirect_uri")
	b.set("hide_credentials", false)
	b.set("upstream_access_token_header", defaultUpstreamAccessTokenHeader)
	b.set("anonymous", managerAnonymousConsumerName)
	b.set("logout_query_arg", tvManagerOIDCLogoutQueryArg) // must match ?logout=true in manager page
	b.forceAuthMethods("The credential manager page route uses [session], "+
		"because an anonymous caller must reach the plugin login page.",
		tvOIDCSessionMethod)
	b.applyTokenEndpointAuthMethod()
	return b.finish()
}

// tvManagerLoginOIDCConfig returns the openid-connect config of the Token
// Vault credential manager login route. An anonymous caller starts the
// authorization code flow here. The strategy redirect_uri names the route
// that completes the exchange. Without one the plugin derives the redirect
// URI from the request, and the exchange completes on this route.
func (c *Converter) tvManagerLoginOIDCConfig(
	src ConversionIssueSource, idp *aigw.AuthStrategy,
) (map[string]any, error) {
	b := c.tvOIDC(src, idp)
	b.remove(tvManagerOIDCRemovedFields...)
	b.remove("anonymous")
	b.set("hide_credentials", false)
	b.set("upstream_access_token_header", defaultUpstreamAccessTokenHeader)
	b.forceAuthMethods("The credential manager login route uses [session, authorization_code], "+
		"because the route starts the browser login.",
		tvOIDCSessionMethod, tvOIDCAuthCodeMethod)
	b.applyTokenEndpointAuthMethod()
	b.set("authorization_cookie_path", c.managerAuthorizationCookiePath(b.cfg, idp))
	// The redirect mode is fragment, and nothing in this flow reads it. An
	// empty login_tokens keeps the signed id token out of the browser URL.
	if target := managerRedirectTarget(b.cfg["redirect_uri"], idp.Name); target != nil {
		b.set("login_action", "redirect")
		b.set("login_redirect_uri", []any{target.loginRedirectURI})
		b.set("login_methods", []any{tvOIDCSessionMethod, tvOIDCAuthCodeMethod})
		b.set("login_tokens", []any{})
	}
	if method, _ := b.cfg["token_endpoint_auth_method"].(string); method == tvOIDCPublicClientAuthMethod {
		b.set("require_proof_key_for_code_exchange", true)
	}
	return b.finish()
}

// managerAuthorizationCookiePath returns the path that the authorization
// cookie of the login route must reach. A strategy redirect_uri sends the
// exchange to its own path. Without one the exchange completes on the login
// route, and the page route path covers both routes.
func (c *Converter) managerAuthorizationCookiePath(cfg map[string]any, idp *aigw.AuthStrategy) string {
	if target := managerRedirectTarget(cfg["redirect_uri"], idp.Name); target != nil {
		return target.cookiePath
	}
	return aimap.TokenVaultCredentialManagerPath + "/" + idp.Name
}

// managerRedirectTarget holds the paths of a manager login flow that a
// strategy redirect_uri drives.
type managerRedirectTargetData struct {
	cookiePath       string
	loginRedirectURI string
}

// managerRedirectTarget reads the strategy redirect_uri. It reports nil for
// an unset or relative value, because the plugin derives the redirect URI
// from the request.
func managerRedirectTarget(redirect any, strategyName string) *managerRedirectTargetData {
	uri := firstConfigString(map[string]any{"redirect_uri": redirect}, "redirect_uri")
	if uri == "" {
		return nil
	}
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return nil
	}
	cookiePath := u.Path
	if cookiePath == "" {
		cookiePath = "/"
	}
	return &managerRedirectTargetData{
		cookiePath:       cookiePath,
		loginRedirectURI: u.Scheme + "://" + u.Host + aimap.TokenVaultCredentialManagerPath + "/" + strategyName,
	}
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
		if secret, err = c.sessionSecret(); err != nil {
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

// sessionSecret returns the configured session secret generator, or the
// default random one.
func (c *Converter) sessionSecret() (string, error) {
	if c.opts.SessionSecret != nil {
		return c.opts.SessionSecret()
	}
	return newSessionSecret()
}

// tvManagerOIDCRemovedFields are the openid-connect fields that the
// credential manager routes must have at their defaults. The page route
// sets anonymous, and the login route keeps a strategy-set redirect_uri.
var tvManagerOIDCRemovedFields = []string{
	"authorization_cookie_same_site",
	"authorization_cookie_samesite",
	"login_redirect_uri",
	"session_cookie_path",
	"session_cookie_same_site",
	"session_cookie_samesite",
}

// Callbacks for Token Vault enrollment require specific configuration to avoid
// clobbering the enrollment flow itself, so unlike the self-serve manager,
// we enforce the callback to return to /_vault/callback/initiate rather than
// adopting the auth strategy's redirect_uri (if available)
var tvEnrollmentOIDCRemovedFields = slices.Concat(
	tvManagerOIDCRemovedFields, []string{"anonymous", "redirect_uri"})

const (
	// tvEnrollmentOIDCLoginAction makes the login redirect carry the session
	// cookie on the plugin's own response. Any other login_action lets the
	// callback handler's 302 replace that Set-Cookie, and the caller loses the
	// session mid-flow.
	tvEnrollmentOIDCLoginAction = "redirect"

	// tvOIDCPublicClientAuthMethod is the token endpoint
	// authentication method of a client with no secret.
	tvOIDCPublicClientAuthMethod = "none"

	// tvManagerOIDCLogoutQueryArg is the logout query argument used by the
	// credential manager page.
	tvManagerOIDCLogoutQueryArg = "logout"

	tvOIDCSessionMethod  = "session"
	tvOIDCAuthCodeMethod = "authorization_code"
)

// tvOIDC builds the openid-connect config of a Token Vault route from
// an auth strategy. It reports each field that it removes or replaces.
type tvOIDC struct {
	c   *Converter
	src ConversionIssueSource
	idp *aigw.AuthStrategy
	cfg map[string]any
}

func (c *Converter) tvOIDC(src ConversionIssueSource, idp *aigw.AuthStrategy) *tvOIDC {
	cfg := maps.Clone(idp.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	return &tvOIDC{c: c, src: src, idp: idp, cfg: cfg}
}

// remove deletes each field and reports the ones that the strategy sets.
func (b *tvOIDC) remove(fields ...string) {
	for _, field := range fields {
		if !isEmptyConfigValue(b.cfg[field]) {
			reason := "auth strategy %q sets config.%s. The Token Vault enrollment routes " +
				"leave it out, because the enrollment flow needs the default value."
			if field == "redirect_uri" {
				reason = "auth strategy %q sets config.%s. The Token Vault enrollment routes " +
					"leave it out. The authorization server must recognize the enrollment route URL " +
					"(gateway-hosted /_vault/callback/initiate)."
			}
			b.report(ConfigFieldIgnored, field, reason, b.idp.Name, field)
		}
		delete(b.cfg, field)
	}
}

// set replaces a field and reports a different value that the strategy sets.
func (b *tvOIDC) set(field string, value any) {
	if old := b.cfg[field]; !isEmptyConfigValue(old) && !reflect.DeepEqual(old, value) {
		b.report(ConfigFieldOverridden, field,
			"auth strategy %q sets config.%s to %v. The Token Vault enrollment routes use %v, "+
				"because the browser flow needs that value.",
			b.idp.Name, field, old, value)
	}
	b.cfg[field] = value
}

// forceAuthMethods sets auth_methods and reports a different list that the
// strategy sets. A Token Vault route must list its methods, because the
// plugin default enables every method.
func (b *tvOIDC) forceAuthMethods(reason string, methods ...string) {
	if old := configStrings(b.cfg, "auth_methods"); len(old) > 0 && !slices.Equal(old, methods) {
		b.report(ConfigFieldOverridden, "auth_methods",
			"auth strategy %q sets config.auth_methods to %v. "+reason,
			b.idp.Name, old)
	}
	out := make([]any, len(methods))
	for i, m := range methods {
		out[i] = m
	}
	b.cfg["auth_methods"] = out
}

// applyTokenEndpointAuthMethod sets the token endpoint authentication method
// of a public client. A strategy with a client secret, or with its own value,
// keeps what it has. The plugin then picks a method from the discovery
// document.
func (b *tvOIDC) applyTokenEndpointAuthMethod() {
	if !isEmptyConfigValue(b.cfg["token_endpoint_auth_method"]) ||
		!isEmptyConfigValue(b.cfg["client_secret"]) {
		return
	}
	b.cfg["token_endpoint_auth_method"] = tvOIDCPublicClientAuthMethod
}

// finish adds the session secret and returns the config.
func (b *tvOIDC) finish() (map[string]any, error) {
	if err := b.c.applySessionSecret(b.src, b.idp, b.cfg); err != nil {
		return nil, err
	}
	return b.cfg, nil
}

// report records one issue per auth strategy field. Every Token Vault route
// rewrites the same strategy config, so the same field arrives many times.
// A strategy that no entity uses makes no output, so it reports nothing.
func (b *tvOIDC) report(code ConversionIssueCode, field, format string, args ...any) {
	if b.c.tvOIDCSilent {
		return
	}
	key := b.idp.Name + "\x00" + field
	if b.c.tvOIDCWarned[key] {
		return
	}
	b.c.tvOIDCWarned[key] = true
	b.c.warn(b.src.at("config."+field), code, format, args...)
}

// tvEnrollmentOIDCConfig returns the openid-connect config of the Token Vault
// enrollment start route. A sessionless browser logs in here. The login
// returns to this path, which is the one redirect URI that the authorization
// server knows, and login_action then sends the browser back to the start URL
// with the enrollment token.
func (c *Converter) tvEnrollmentOIDCConfig(src ConversionIssueSource, idp *aigw.AuthStrategy) (map[string]any, error) {
	b := c.tvOIDC(src, idp)
	b.remove(tvEnrollmentOIDCRemovedFields...)
	b.set("hide_credentials", false)
	b.set("upstream_access_token_header", defaultUpstreamAccessTokenHeader)
	b.set("preserve_query_args", true)
	b.set("authorization_cookie_path", aimap.TokenVaultCallbackInitiatePath)
	b.set("login_action", tvEnrollmentOIDCLoginAction)
	b.set("login_methods", []any{tvOIDCAuthCodeMethod})
	// The handler keeps the query args; the default login_tokens would
	// append the signed id token to the redirect URL fragment.
	b.set("login_tokens", []any{})
	b.applyTokenEndpointAuthMethod()
	b.forceAuthMethods("The enrollment start route uses [session, authorization_code], "+
		"because the route starts the browser login.",
		tvOIDCSessionMethod, tvOIDCAuthCodeMethod)
	return b.finish()
}

// secureTokenVaultCallbackRoute adds the enrollment start route with its
// openid-connect plugin. The first used strategy with the Datastore of the
// callback route gives the config. The callback handler then binds each
// enrollment to the principal that starts it. The completion path stays on
// the open /_vault route: the state cookie protects its single-use code, and
// a login there would discard the code.
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
	initiate, err := c.tvEnrollmentOIDCConfig(src, chosen)
	if err != nil {
		// The /_vault route would serve an unprotected start path.
		c.out.Routes = slices.DeleteFunc(slices.Clone(c.out.Routes), func(r kong.Route) bool {
			return r.Name == aimap.TokenVaultCallbackName || r.Name == aimap.TokenVaultCallbackInitiateName
		})
		return
	}
	callback := c.out.Routes[i]
	c.out.Routes = slices.Insert(slices.Clone(c.out.Routes), i+1, kong.Route{
		Name:      aimap.TokenVaultCallbackInitiateName,
		Paths:     []string{aimap.TokenVaultCallbackInitiatePath},
		Protocols: []string{"http", "https"},
		Plugins: []kong.Plugin{
			{
				Name:   aimap.TokenVaultCallbackName,
				Config: maps.Clone(callback.Plugins[0].Config),
				Source: callback.Plugins[0].Source,
			},
			{
				Name:   authStrategyTypeOpenIDConnect,
				Config: initiate,
				Source: source(kindAuthStrategy, chosen.Name, "datastores"),
			},
		},
		Source: callback.Source,
	})
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

// credentialManagerRoute adds the routes of the auth strategy that serve the
// Token Vault credential manager. The page route renders the plugin page, and
// the login route starts the authorization code flow of the browser login.
// Both routes carry the credential manager, because the page route also
// serves the plugin data, and the login route serves the page after a login
// that completes on it.
func (c *Converter) credentialManagerRoute(
	src ConversionIssueSource, idp *aigw.AuthStrategy, ds *aigw.Datastore,
) error {
	name := aimap.TokenVaultCredentialManagerName + "-" + idp.Name
	for _, suffix := range []string{"", "-login"} {
		for _, route := range c.out.Routes {
			if route.Name == name+suffix {
				return nil
			}
		}
	}
	pageConfig, err := c.tvManagerOIDCConfig(src, idp)
	if err != nil {
		return err
	}
	loginConfig, err := c.tvManagerLoginOIDCConfig(src, idp)
	if err != nil {
		return err
	}
	config, err := aimap.ApplyTokenVaultDatastore(nil, ds)
	if err != nil {
		return c.fail(src.at("datastores[0].name"), ConfigFieldInvalid, "auth strategy %q: %v", idp.Name, err)
	}
	routeSource := source(kindAuthStrategy, idp.Name, "token_vault")
	base := aimap.TokenVaultCredentialManagerPath + "/" + idp.Name
	managerPlugin := kong.Plugin{
		Name:   aimap.TokenVaultCredentialManagerName,
		Config: config,
		Source: routeSource,
	}
	oidcPlugin := func(cfg map[string]any) kong.Plugin {
		return kong.Plugin{Name: authStrategyTypeOpenIDConnect, Config: cfg, Source: routeSource}
	}
	c.ensureManagerAnonymousConsumer()
	c.out.Routes = append(c.out.Routes,
		kong.Route{
			Name:      name,
			Paths:     []string{base},
			Protocols: []string{"http", "https"},
			Plugins:   []kong.Plugin{oidcPlugin(pageConfig), managerPlugin},
			Source:    routeSource,
		},
		kong.Route{
			Name:      name + "-login",
			Paths:     []string{base + "/login"},
			Protocols: []string{"http", "https"},
			Plugins:   []kong.Plugin{oidcPlugin(loginConfig), managerPlugin},
			Source:    routeSource,
		},
	)
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
		if c.out.Consumers[i].Username != anonymousConsumerName &&
			c.out.Consumers[i].CustomID != anonymousConsumerName {
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

// ensureManagerAnonymousConsumer appends the Consumer that the credential
// manager page route falls back to. It carries no request-termination plugin,
// because the unauthenticated caller must reach the credential manager. The
// credential manager rejects the data requests itself.
func (c *Converter) ensureManagerAnonymousConsumer() {
	for i := range c.out.Consumers {
		if c.out.Consumers[i].Username == managerAnonymousConsumerName ||
			c.out.Consumers[i].CustomID == managerAnonymousConsumerName {
			return
		}
	}
	c.out.Consumers = append(c.out.Consumers, kong.Consumer{
		Username: managerAnonymousConsumerName,
		CustomID: managerAnonymousConsumerName,
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
