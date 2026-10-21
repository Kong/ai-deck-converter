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
		c.warnAuthStrategyOverrides(idp)
		plugin := authStrategyPlugin(idp)
		if err := c.applyAuthStrategyTokenVault(idp, &plugin); err != nil {
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

// validateAuthStrategyTokenVaults reports the token_vault errors of every auth
// strategy, also of a strategy that no entity references.
// The output does not keep the callback route of an unused strategy.
func (c *Converter) validateAuthStrategyTokenVaults() {
	for i := range c.src.AuthStrategies {
		snapshot := *c.out
		probe := kong.Plugin{Config: map[string]any{}}
		_ = c.applyAuthStrategyTokenVault(&c.src.AuthStrategies[i], &probe)
		*c.out = snapshot
	}
}

// applyAuthStrategyTokenVault sets config.token_vault of the openid-connect
// plugin from the auth strategy and adds the Token Vault callback route.
// Any other strategy type with a token_vault fails.
func (c *Converter) applyAuthStrategyTokenVault(idp *aigw.AuthStrategy, plugin *kong.Plugin) error {
	src := issueSource(kindAuthStrategy, idp.Name)
	if idp.TokenVault == nil {
		return nil
	}
	if idp.Type != authStrategyTypeOpenIDConnect {
		return c.fail(src.at("token_vault"), ConfigFieldInvalid,
			"auth strategy %q: token_vault requires type %q, not %q",
			idp.Name, authStrategyTypeOpenIDConnect, idp.Type)
	}
	if enabled, _ := idp.TokenVault[enableCredentialManagerKey].(bool); !enabled {
		return nil
	}
	if !c.supports(aimap.FeatureAuthStrategyTokenVault) {
		return c.failUnsupported(src.at("token_vault"), aimap.FeatureAuthStrategyTokenVault, "token_vault")
	}
	if hide, _ := idp.Config["hide_credentials"].(bool); hide {
		return c.fail(src.at("config.hide_credentials"), ConfigFieldInvalid,
			"auth strategy %q: config.hide_credentials must not be true with token_vault",
			idp.Name)
	}
	if len(idp.Datastores) != 1 {
		return c.fail(src.at("datastores"), MissingRequiredConfigField,
			"auth strategy %q: token_vault requires exactly one %q datastore",
			idp.Name, aimap.DatastoreTypeTokenVault)
	}
	ds := c.datastores[idp.Datastores[0].Name]
	if ds == nil {
		return c.fail(src.at("datastores[0].name"), UndefinedReference,
			"auth strategy %q references unknown datastore %q", idp.Name, idp.Datastores[0].Name)
	}
	if ds.Type != aimap.DatastoreTypeTokenVault {
		return c.fail(src.at("datastores[0].name"), ConfigFieldInvalid,
			"auth strategy %q: token_vault does not support a %q datastore; use %q instead",
			idp.Name, ds.Type, aimap.DatastoreTypeTokenVault)
	}
	settings := maps.Clone(idp.TokenVault)
	delete(settings, enableCredentialManagerKey)
	block, err := aimap.ApplyTokenVaultDatastore(settings, ds)
	if err != nil {
		return c.fail(src.at("datastores"), ConfigFieldInvalid, "auth strategy %q: %v", idp.Name, err)
	}
	owner := fmt.Sprintf("auth strategy %q", idp.Name)
	routeSource := source(kindAuthStrategy, idp.Name, "datastores")
	if err := c.ensureTokenVaultCallbackRoute(src.at("datastores[0].name"), owner, ds, routeSource); err != nil {
		return err
	}
	if err := c.credentialManagerRoute(src, idp, ds); err != nil {
		return err
	}
	if err := c.applyTokenVaultOIDCDefaults(src, idp, plugin.Config); err != nil {
		return err
	}
	block["enabled"] = true
	plugin.Config["token_vault"] = block
	return nil
}

// applyTokenVaultOIDCDefaults sets the openid-connect fields that an enabled
// Token Vault strategy needs and does not set. Every plugin of the strategy
// gets the same generated session secret.
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

// credentialManagerRoute adds the route of the auth strategy that serves the
// Token Vault credential manager. The route has its own openid-connect plugin
// with the unchanged config of the strategy.
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
	config := map[string]any{}
	if directory, ok := ds.Config["directory"]; ok {
		config["directory"] = directory
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
			"auth strategy %q sets config.anonymous to %v. "+
				"This will be ignored, and anonymous will be overridden to %q "+
				"to reject unauthenticated requests with a request-termination plugin.",
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
