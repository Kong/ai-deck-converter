package convert

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// routeGroup accumulates everything that maps to a single Kong route, keyed by
// (section, routeLabel, route configuration). The route and its
// ai-model-selector are shared only by models with the same endpoint and
// client-facing route configuration; each owning model contributes its own
// ai-proxy-advanced plugin (a proxyGroup).
type routeGroup struct {
	route kong.Route
	// websocket routes belong to the WebSocket Service. They carry no
	// ai-model-selector. Their plugins are therefore route-scoped.
	websocket bool
	// selectorOrder holds dedup keys (json of entry minus max_request_body_size),
	// in first-contributed order.
	selectorOrder []string
	// selectorByKey maps a dedup key to that entry (minus max_request_body_size).
	selectorByKey map[string]map[string]any
	selectorMax   int // largest max_request_body_size across contributors that set it
	// selectorUnlimited is true when a contributor sets max_request_body_size to 0.
	// The plugin reads 0 as unlimited, so 0 wins over every other value.
	selectorUnlimited bool
	proxies           []*proxyGroup
	proxyByOwner      map[string]*proxyGroup
	// models holds the names of the models on the route, in join order.
	models []string
	// selectorShapes holds the selector shape keys of each model on the route.
	selectorShapes map[string][]string
	// aliasOwners maps each alias on the route to the first model that uses it.
	aliasOwners map[string]string
}

// addSelector folds one model's desired ai-model-selector shape into the
// route's accumulated set of sources, deduping identical shapes and merging
// max_request_body_size across every body-shaped contributor: the group's
// selector reads one shared byte ceiling regardless of which source entry
// ends up matching a given request.
func (g *routeGroup) addSelector(cfg map[string]any) {
	if cfg == nil {
		return
	}
	entry := make(map[string]any, len(cfg))
	maps.Copy(entry, cfg)
	if size, ok := entry["max_request_body_size"].(int); ok {
		delete(entry, "max_request_body_size")
		if size == 0 {
			g.selectorUnlimited = true
		}
		if size > g.selectorMax {
			g.selectorMax = size
		}
	}

	keyBytes, err := json.Marshal(entry)
	if err != nil {
		return
	}
	key := string(keyBytes)
	if _, seen := g.selectorByKey[key]; !seen {
		if g.selectorByKey == nil {
			g.selectorByKey = map[string]map[string]any{}
		}
		g.selectorByKey[key] = entry
		g.selectorOrder = append(g.selectorOrder, key)
	}
}

// selectorConfig assembles the ai-model-selector plugin config from every
// distinct shape contributed by the route's models, plus a single
// max_request_body_size raised to the largest ceiling any body-shaped entry
// asked for. Returns nil when no model on the route wants a selector at all.
// It emits config.sources even for a single shape.
// Data planes that read config.sources reject the legacy config.source.
func (g *routeGroup) selectorConfig() map[string]any {
	if len(g.selectorOrder) == 0 {
		return nil
	}

	sources := make([]map[string]any, 0, len(g.selectorOrder))
	for _, key := range g.selectorOrder {
		sources = append(sources, g.selectorByKey[key])
	}

	cfg := map[string]any{"sources": sources}
	g.setSelectorBodySize(cfg)

	return cfg
}

func (g *routeGroup) setSelectorBodySize(cfg map[string]any) {
	switch {
	case g.selectorUnlimited:
		cfg["max_request_body_size"] = 0
	case g.selectorMax > 0:
		cfg["max_request_body_size"] = g.selectorMax
	}
}

// proxyGroup accumulates one ai-proxy-advanced plugin: the targets owned by a
// single source model on a route (type "model", scoped to route+ai-model), or
// every target on a route (type "api", scoped route-only and merged).
type proxyGroup struct {
	routeName string
	// aliases are the ai-model FKs this proxy group's targets are emitted
	// under; empty scopes the plugin route-only (type "api"). One
	// ai-proxy-advanced plugin is emitted per alias (see the emission loop),
	// each with its targets' model_alias set to that alias.
	aliases []string
	// realtimeModel names the ai_models entry of a route-scoped realtime plugin.
	// Its targets carry no model_alias. No selector presents an alias on a
	// WebSocket route, so an aliased target is in a pool nothing selects.
	realtimeModel     string
	llmFormat         string
	genaiCategory     string
	balancer          map[string]any
	vectordb          any
	datastoreName     string // datastore whose connection was injected into vectordb; empty when authored inline
	tokenVault        map[string]any
	embeddings        any
	responseStreaming string
	modelNameHeader   *bool
	maxBodySize       *int
	proxy             map[string]any
	targets           []map[string]any
	targetSources     []kong.TargetSource
	source            *kong.Source
	seen              map[string]int // target fingerprint -> index into targets/targetSources
}

type videoLifecycleTarget struct {
	target       *aigw.TargetModel
	provider     *aigw.Provider
	providerType string
}

// videoLifecycleCandidate owns an OpenAI-format video route. Its lifecycle
// proxy is static (not model-scoped) but retains every creation target.
type videoLifecycleCandidate struct {
	index   int
	model   *aigw.Model
	targets []videoLifecycleTarget
	spec    aimap.EndpointSpec
}

// modelPolicyRef names a policy reference of a model by its field path.
type modelPolicyRef struct {
	model string
	field string
}

// modelPass holds the state that the models of one conversion pass share.
type modelPass struct {
	groups              map[string]*routeGroup
	order               []string
	guardPlugins        []kong.Plugin
	usedRouteNames      map[string]bool
	passthroughMatchers map[string]string
	identityPluginSeen  map[string]bool
	// websocketRoutes holds the protocols of each WebSocket route by name.
	// Plugins on these routes must declare the same protocols to run.
	websocketRoutes map[string][]string
	// selectorBefore holds, per route, the model policy plugin names that
	// must run after that route's ai-model-selector.
	selectorBefore map[string][]string
	// selectorAfter holds, per route, the plugins that reject unauthenticated
	// requests. A selector that carries selectorBefore still runs after them.
	selectorAfter map[string][]string
	// requestTerminations holds, per route, the model-scoped request-termination
	// policies that the selector ordering stops from running.
	requestTerminations map[string][]modelPolicyRef
	// aiModelRows maps each ai_models name of the pass to the model that made
	// the entry.
	aiModelRows map[string]aiModelRow
	// selectorConflicts holds the model pairs with a reported selector conflict.
	selectorConflicts map[string]bool
	// aliasConflicts holds the model and alias pairs with a reported alias
	// conflict.
	aliasConflicts map[string]bool
	// skipped holds the issues of each model index that a pass left out.
	skipped map[int][]ConversionIssue
	// tokenVaults holds the token-vault-exchange config of each model.
	tokenVaults map[*aigw.Model]map[string]any
}

// convertModels groups all (model, target, capability) tuples into routes under
// a single shared ai-gateway Service, emitting a route-scoped ai-model-selector
// and one ai-proxy-advanced per owning model (type "model") or per route (type
// "api"), plus an ai-models entry per source model. type "model" plugins are
// scoped to both the route and the ai-model entity; type "api" plugins are
// scoped route-only.
//
// Models share routes and plugins. A model that fails can have changed them
// already, so a failure starts a new pass that leaves out that model. The
// copy of c.out is shallow, so a model must not change an element of a c.out
// slice in place. The
// restart cost grows with the number of models times the number of failed
// models.
func (c *Converter) convertModels() {
	out := *c.out
	issueCount := len(c.issues)
	skipped := map[int][]ConversionIssue{}
	for !c.convertModelsPass(skipped) {
		*c.out = out
		c.issues = c.issues[:issueCount]
	}
}

// convertModelsPass converts every model not in skipped. When a model fails,
// it adds the model to skipped and returns false.
func (c *Converter) convertModelsPass(skipped map[int][]ConversionIssue) bool {
	st := &modelPass{
		groups:              map[string]*routeGroup{},
		usedRouteNames:      map[string]bool{},
		passthroughMatchers: map[string]string{},
		identityPluginSeen:  map[string]bool{},
		websocketRoutes:     map[string][]string{},
		selectorBefore:      map[string][]string{},
		selectorAfter:       map[string][]string{},
		requestTerminations: map[string][]modelPolicyRef{},
		aiModelRows:         map[string]aiModelRow{},
		selectorConflicts:   map[string]bool{},
		aliasConflicts:      map[string]bool{},
		skipped:             skipped,
		tokenVaults:         map[*aigw.Model]map[string]any{},
	}
	c.modelRouteModels = map[string][]string{}
	lifecycleCandidates := c.videoLifecycleCandidates(skipped)

	for i := range c.src.Models {
		m := &c.src.Models[i]
		if !c.convertModelOnce(st, i, func() error { return c.convertModel(st, m) }) {
			return false
		}
	}

	if len(st.order) == 0 {
		c.out.Plugins = append(c.out.Plugins, st.guardPlugins...)
		return true
	}

	service := kong.Service{Name: aimap.GatewayServiceName, URL: aimap.GatewayServiceURL}
	websocketService := kong.Service{Name: aimap.GatewayWebSocketServiceName, URL: aimap.GatewayWebSocketServiceURL}
	for _, key := range st.order {
		g := st.groups[key]
		c.modelRouteModels[g.route.Name] = g.models
		if g.websocket {
			websocketService.Routes = append(websocketService.Routes, g.route)
		} else {
			service.Routes = append(service.Routes, g.route)
		}

		if selectorCfg := g.selectorConfig(); selectorCfg != nil {
			mappings := []kong.FieldMapping{
				{GeneratedPrefix: "config.max_request_body_size", SourcePrefix: "config.max_request_body_size"},
				{GeneratedPrefix: "config.sources", SourcePrefix: "config.route.model"},
			}
			c.out.Plugins = append(c.out.Plugins, kong.Plugin{
				Name:     "ai-model-selector",
				Route:    kong.NewStringRef(g.route.Name),
				Config:   selectorCfg,
				Ordering: c.selectorOrdering(st, g.route.Name),
				Source:   source("model", g.route.Source.EntityName, "config.route.model", mappings...),
			})
		}
		for _, pg := range g.proxies {
			if len(pg.aliases) == 0 {
				var protocols, tags []string
				if g.websocket {
					protocols = g.route.Protocols
				}
				if pg.realtimeModel != "" {
					tags = []string{aimap.EncodeRealtimeModel(pg.realtimeModel)}
				}
				c.out.Plugins = append(c.out.Plugins, kong.Plugin{
					Name:          "ai-proxy-advanced",
					Protocols:     protocols,
					Route:         kong.NewStringRef(pg.routeName),
					Config:        pg.proxyConfig(),
					Tags:          tags,
					TargetSources: pg.targetSources,
					Source:        pg.source,
				})
				c.appendTokenVaultExchange(pg, nil)
				continue
			}
			// One plugin per alias: ai-model-selector activates a model-scoped
			// plugin by matching the request's model string against a single
			// ai_models row, so each alias needs its own plugin/FK, with its
			// targets' model_alias set to that same alias.
			for _, alias := range pg.aliases {
				cfg := pg.proxyConfig()
				cfg["targets"] = withModelAlias(pg.targets, alias, pg.llmFormat)
				c.out.Plugins = append(c.out.Plugins, kong.Plugin{
					Name:          "ai-proxy-advanced",
					Route:         kong.NewStringRef(pg.routeName),
					Config:        cfg,
					Model:         kong.NewStringRef(alias),
					TargetSources: pg.targetSources,
					Source:        pg.source,
				})
				c.appendTokenVaultExchange(pg, kong.NewStringRef(alias))
			}
		}
	}
	for _, candidate := range lifecycleCandidates {
		if !c.convertModelOnce(st, candidate.index, func() error {
			return c.convertVideoLifecycle(st, candidate, &service)
		}) {
			return false
		}
	}
	for _, svc := range []kong.Service{service, websocketService} {
		if len(svc.Routes) > 0 {
			c.out.Services = append(c.out.Services, svc)
		}
	}
	c.out.Plugins = append(c.out.Plugins, st.guardPlugins...)
	return true
}

// appendTokenVaultExchange adds the token-vault-exchange plugin of pg with
// the scope of its ai-proxy-advanced plugin.
func (c *Converter) appendTokenVaultExchange(pg *proxyGroup, model *kong.StringRef) {
	if pg.tokenVault == nil {
		return
	}
	c.out.Plugins = append(c.out.Plugins, kong.Plugin{
		Name:   tokenVaultExchangePlugin,
		Route:  kong.NewStringRef(pg.routeName),
		Model:  model,
		Config: pg.tokenVault,
		Source: source(kindModel, pg.source.EntityName, "targets"),
	})
}

// deriveTokenVaultExchangeInbound fills the token-vault-exchange inbound
// credential from the model's first key-auth strategy, or from a global
// key-auth policy when no strategy supplies it. The plugin default inbound
// Authorization Bearer does not match where key-auth expects the credential.
// An explicit inbound is never touched.
func (c *Converter) deriveTokenVaultExchangeInbound(
	src ConversionIssueSource, m *aigw.Model, provider, field string, cfg map[string]any,
) {
	cred, _ := cfg["credential"].(map[string]any)
	if cred != nil && cred["inbound"] != nil {
		return
	}
	apply := func(kind, name string, keyAuth map[string]any) *aigw.TokenVaultCredentialLocation {
		loc := aimap.TokenVaultInboundFromKeyAuth(keyAuth)
		if loc == nil {
			// The warning recurs on a re-conversion, whose reverter
			// synthesizes strategy and policy names. It names only the kind.
			c.warn(src.at(field), BehaviorMayDiffer,
				"Model %q: the %s does not read its key from the headers, so "+
					"token_vault.credential.inbound cannot be derived from it. The data plane keeps "+
					"the default inbound (Authorization: Bearer), which the exchange will not find.",
				m.Name, kind)
			return nil
		}
		if cred == nil {
			cred = map[string]any{}
			cfg["credential"] = cred
		}
		cred["inbound"] = aimap.TokenVaultLocationToPlugin(loc)
		c.warn(src.at(field), ConfigFieldOverridden,
			"Model %q: token_vault.credential.inbound of provider %q is derived from the %s %q "+
				"(header %q, its first key name, no prefix), because the plugin default "+
				"Authorization: Bearer does not match where key-auth reads the key.",
			m.Name, provider, kind, name, loc.Name)
		return loc
	}
	idp := c.firstKeyAuthStrategy(m.Access.AuthStrategies)
	gpol := c.globalKeyAuthPolicy()
	switch {
	case idp != nil:
		loc := apply("key-auth strategy", idp.Name, idp.Config)
		if loc == nil || gpol == nil {
			return
		}
		if gloc := aimap.TokenVaultInboundFromKeyAuth(gpol.Config); gloc == nil || gloc.Name != loc.Name {
			reads := "the request body"
			if gloc != nil {
				reads = fmt.Sprintf("header %q", gloc.Name)
			}
			// The warning recurs on a re-conversion, whose reverter
			// synthesizes policy names. It names only the header names.
			c.warn(src.at(field), BehaviorMayDiffer,
				"Model %q: the global key-auth policy reads its key from %s, but the derived "+
					"inbound reads header %q. The exchange uses only the derived inbound.",
				m.Name, reads, loc.Name)
		}
	case gpol != nil:
		apply("global key-auth policy", gpol.Name, gpol.Config)
	}
}

// firstKeyAuthStrategy returns the first key-auth strategy of refs, or nil.
func (c *Converter) firstKeyAuthStrategy(refs []string) *aigw.AuthStrategy {
	for _, ref := range refs {
		if idp := c.authStrategies[ref]; idp != nil && idp.Type == "key-auth" {
			return idp
		}
	}
	return nil
}

// convertModelOnce runs fn for the model at index i. A skipped model only
// replays its issues. When fn fails, it records the model as skipped and
// returns false.
func (c *Converter) convertModelOnce(st *modelPass, i int, fn func() error) bool {
	if issues, ok := st.skipped[i]; ok {
		c.issues = append(c.issues, issues...)
		return true
	}
	start := len(c.issues)
	if err := c.guard(issueSource(kindModel, c.src.Models[i].Name), fn); err != nil {
		st.skipped[i] = slices.Clone(c.issues[start:])
		return false
	}
	return true
}

// convertModel adds the routes, targets, and plugins of m to the pass.
func (c *Converter) convertModel(st *modelPass, m *aigw.Model) error {
	src := issueSource(kindModel, m.Name)
	// A disabled model makes no output. Its aliases and routes would
	// otherwise conflict with enabled models.
	if isDisabled(m) {
		return nil
	}
	bases := basePaths(m)

	// extractModelAliases yields the name(s) a client uses to select this
	// model: authored aliases, or the model name when none is authored. They
	// seed both the ai-models entity identities and each type:model target's
	// model_alias; type:api targets stay alias-less — see the gate at the
	// buildTarget call below. Each alias gets its own ai-models entry and its
	// own ai-proxy-advanced plugin (see the emission loop), since
	// ai-model-selector can only match a request to one ai-models row at a
	// time.
	aliases := c.extractModelAliases(src, m)

	// ownerKey groups targets into ai-proxy-advanced plugins: per source model
	// for type "model" (each carries its own ai-model FK), shared for type
	// "api" (route-only, all targets merged into one plugin).
	modelScoped := isModelType(m)
	// A passthrough model has no ai-model identity on the data plane. Its route carries
	// no ai-model-selector, so nothing sets ctx.ai_model and a model-scoped plugin would never
	// be selected. Every plugin such a model produces is route-scoped instead, exactly as
	// a type "api" model's is. The ai_models row stays: it is the model's identity, and
	// revert recovers the ID and labels from it.
	passthrough := isPassthrough(m)
	fkScoped := modelScoped && !passthrough
	if passthrough {
		// Passthrough is a property of the whole model's traffic, not one wire
		// format among several a route could accept.
		if len(m.Formats) > 1 {
			return c.fail(src.at("formats"), ConfigFieldInvalid,
				"model %q: the passthrough format cannot be combined with other formats", m.Name)
		}
		c.warnPassthroughPolicies(src, m)
		// ai-proxy-advanced refuses the semantic algorithm alongside a passthrough
		// target: choosing semantically means reading a request body whose shape
		// passthrough does not define.
		if balancerAlgorithm(m.Config.Balancer) == "semantic" {
			return c.fail(src.at("formats", "config.balancer.algorithm"), ConfigFieldInvalid,
				"model %q: the passthrough format cannot use the semantic balancer algorithm",
				m.Name)
		}
		// Kong allows one plugin of a name per route, and without a selector nothing
		// tells two ai-proxy-advanced plugins on one route apart. Two passthrough
		// models whose routes match the same requests are just as ambiguous even when
		// they land in different route groups (different auth strategies, route
		// names, ...), so the check keys on the request matchers, not the group key.
		for _, b := range bases {
			key, err := passthroughMatcherKey(b, m.Config.Route)
			if err != nil {
				return err
			}
			if other, ok := st.passthroughMatchers[key]; ok && other != m.Name {
				return c.fail(src.at("config.route.paths"), RoutingConflict,
					"model %q: a passthrough model cannot share route path %q with passthrough model %q",
					m.Name, b, other)
			}
			st.passthroughMatchers[key] = m.Name
		}
	}
	lists, err := c.modelLists(src, m)
	if err != nil {
		return err
	}
	if err := c.checkModelFeatures(src, m); err != nil {
		return err
	}
	// A passthrough model serves one route, PassthroughEndpoint, whatever
	// capabilities it declares. Its capabilities therefore never gate it.
	if !passthrough {
		if err := c.checkModelCapabilities(src, m, lists); err != nil {
			return err
		}
	}
	ownerKey := ""
	if modelScoped || passthrough {
		ownerKey = m.Name
	}
	ds, err := c.resolveModelDatastore(src, m)
	if err != nil {
		return err
	}
	if err := c.checkEmbeddingsTokenVault(src, m); err != nil {
		return err
	}
	if m.Config.Route.Name != "" {
		c.warn(src.at("config.route.name"), ConfigFieldOverridden,
			"model %q sets config.route.name to %q. "+
				"This will be ignored, and each route name will be overridden "+
				"to the generated {format}-{endpoint} name.",
			m.Name, m.Config.Route.Name)
	}

	var routeNames []string
	routeSeen := map[string]bool{}
	statsWarned := map[string]bool{}
	// The plugin's llm_format comes from the first target, and passthrough forwards
	// the body unchanged, so every target must speak the same client format.
	passthroughFormat := ""

	var targets []plannedTarget
	var tokenVault map[string]any
	for j := range m.TargetModels {
		tm := &m.TargetModels[j]
		provider, providerType, problem := c.checkTarget(m, j)
		if problem != nil {
			c.warn(src.at(problem.field), problem.code, "%s", problem.reason)
			continue
		}
		if _, ok := tm.Config.Options["vpc_endpoint"]; ok && !c.supports(aimap.FeatureBedrockVPCEndpoint) {
			field := fmt.Sprintf("targets[%d].config.vpc_endpoint", j)
			return c.failUnsupported(src.at(field), aimap.FeatureBedrockVPCEndpoint, field)
		}
		if err := c.checkBedrockEndpointType(src, tm, j); err != nil {
			return err
		}
		tv, err := c.providerTokenVault(src, m, j, provider)
		if err != nil {
			return err
		}
		// token-vault-exchange has model scope. It cannot tell targets apart.
		if len(targets) == 0 {
			tokenVault = tv
		} else if !reflect.DeepEqual(tv, tokenVault) {
			return c.fail(src.at(fmt.Sprintf("targets[%d].provider", j)), ConfigFieldInvalid,
				"model %q target %q: every target must use the same provider token_vault",
				m.Name, tm.Name)
		}
		c.warnUnsupportedCostLists(src, tm, j)
		targets = append(targets, plannedTarget{
			index: j, tm: tm, provider: provider, providerType: providerType,
			target: aimapTarget(tm, providerType),
		})
		if passthrough && !aimap.HasNativeFormat(providerType) {
			c.warn(src.at(fmt.Sprintf("targets[%d].config.type", j)), BehaviorMayDiffer,
				"model %q target %q: provider type %q has no native llm_format, so the passthrough "+
					"format may not extract usage or cost", m.Name, tm.Name, providerType)
		}
		if passthrough {
			format := aimap.ClientFormat(aimap.PassthroughFormat, providerType)
			if passthroughFormat == "" {
				passthroughFormat = format
			} else if format != passthroughFormat {
				return c.fail(src.at(fmt.Sprintf("targets[%d].config.type", j)), ConfigFieldInvalid,
					"model %q target %q: the passthrough format forwards %s bodies unchanged, "+
						"so every target must speak %s, not %s",
					m.Name, tm.Name, passthroughFormat, passthroughFormat, format)
			}
		}
	}

	// Every route of the model carries the same auth strategies. One
	// derivation therefore serves the whole model. The token vault is the
	// first target's, so targets[0] is its producing target.
	if tokenVault != nil {
		c.deriveTokenVaultExchangeInbound(src, m, targets[0].provider.Name,
			fmt.Sprintf("targets[%d].provider.token_vault.credential.inbound", targets[0].index),
			tokenVault)
	}

	formats := c.effectiveFormats(lists, targets)
	pairsByTarget := map[int][]endpointPair{}
	var supportedPairs map[int][]endpointPair
	if passthrough {
		for _, t := range targets {
			pairsByTarget[t.index] = []endpointPair{{
				format: aimap.PassthroughFormat, specs: []aimap.EndpointSpec{aimap.PassthroughEndpoint},
			}}
		}
	} else {
		if len(targets) > 0 {
			if err := c.checkCapabilityEndpoints(src, m, lists, formats); err != nil {
				return err
			}
		}
		for _, t := range targets {
			pairsByTarget[t.index] = c.targetPairs(src, m, lists, formats, t)
		}
		supportedPairs = maps.Clone(pairsByTarget)
		keepEqualPaths(formats, pairsByTarget)
	}

	servedBy := map[string][]int{}
	// routed holds the capabilities that got a route. collapsed holds the
	// first genai category collision of each capability.
	routed := map[string]bool{}
	type collision struct{ category, route, existing string }
	collapsed := map[string]collision{}
	var collapsedOrder []string
	for _, t := range targets {
		j, tm, provider, providerType := t.index, t.tm, t.provider, t.providerType
		for _, pair := range pairsByTarget[j] {
			capability := pair.capability
			if !slices.Contains(servedBy[capability], j) {
				servedBy[capability] = append(servedBy[capability], j)
			}
			sec := aimap.SectionFor(pair.format, providerType)
			format := aimap.ClientFormat(pair.format, providerType)
			// A capability with secondary endpoints (bedrock generate, also
			// reachable via invoke) emits one route per endpoint, each
			// carrying this target; revert's capsSeen folds the routes back
			// into the one capability.
			for _, spec := range pair.specs {
				websocket := aimap.IsWebSocketEndpoint(spec)
				if websocket && tokenVault != nil {
					return c.fail(src.at(capabilityField(m, capability)), ConfigFieldInvalid,
						"model %q: capability %q needs a WebSocket route, but %s does not support "+
							"the ws and wss protocols", m.Name, capability, tokenVaultExchangePlugin)
				}
				// A global plugin keeps Kong's http/https default protocols. A
				// global authentication policy therefore cannot protect a ws or
				// wss route, so the model fails closed.
				if global := c.globalAuthPolicy(); websocket && global != nil {
					return c.fail(src.at(capabilityField(m, capability)), ConfigFieldInvalid,
						"model %q: capability %q needs a WebSocket route, but global policy %q of type %q "+
							"keeps Kong's http/https default protocols and cannot authenticate it",
						m.Name, capability, global.Name, global.Type)
				}
				statsSupported := aimap.LogStatisticsSupported(providerType, spec)
				logging := modelLoggingBlock(withLoggingDefaults(m.Config.Logging, false, false), statsSupported, websocket)
				// Authentication plugins execute before the model selector. Models
				// with different auth-strategy sets therefore cannot share a
				// route: a route-scoped auth plugin would otherwise protect every
				// model on that route.
				identityKey := authStrategyKey(m.Access.AuthStrategies)
				routeConfigKey, err := modelRouteConfigKey(m.Config.Route)
				if err != nil {
					return err
				}
				var selectorCfgs []map[string]any
				if !passthrough && !websocket {
					selectorCfgs = c.buildModelSelectorConfig(src, m, spec)
				}
				// The alias *value* is per-model and deliberately excluded from
				// routeConfigKey. The selector shape does not split routes either.
				// Shapes of models on one route merge into config.sources.
				// A passthrough model's route is its base path whatever its targets'
				// providers, so the section stays out of its key: targets on
				// different providers still share the one route.
				keySection := sec
				if passthrough {
					keySection = ""
				}
				mantlePaths := t.target.Mantle && aimap.MantleMultiPath(format, spec)
				key := keySection + "|" +
					spec.RouteLabel +
					"|" + strconv.FormatBool(mantlePaths) +
					"|" + identityKey +
					"|" + routeConfigKey
				// Without a selector, a route cannot pick one model's plugin.
				// Each model therefore gets its own WebSocket route.
				if websocket {
					key += "|" + m.Name
				}
				g := st.groups[key]
				if g == nil {
					var paths []string
					for _, b := range bases {
						paths = append(paths, aimap.RoutePaths(b, spec, format, mantlePaths)...)
					}
					routeName := uniqueModelRouteName(sec+"-"+spec.RouteLabel, st.usedRouteNames)
					g = &routeGroup{
						route: buildModelRoute(
							m.Config.Route, routeName,
							paths, spec.Methods, websocket),
						websocket:    websocket,
						proxyByOwner: map[string]*proxyGroup{},
					}
					if websocket {
						st.websocketRoutes[routeName] = g.route.Protocols
					}
					g.route.Source = source("model", m.Name, "config.route")
					st.groups[key] = g
					st.order = append(st.order, key)
				}

				for _, selector := range selectorCfgs {
					g.addSelector(selector)
				}
				if !slices.Contains(g.models, m.Name) {
					// Only plugins with an ai_models scope can clash on an alias.
					if fkScoped {
						c.claimRouteAliases(st, src, g, m, aliases)
					}
					g.models = append(g.models, m.Name)
					c.checkSelectorConflict(st, src, g, m.Name, selectorCfgs)
				}
				if !routeSeen[g.route.Name] {
					routeSeen[g.route.Name] = true
					routeNames = append(routeNames, g.route.Name)
					c.warnStatisticsOverride(src, m, g.route.Name, statsSupported, statsWarned)
					if websocket && loggingPayloadsSet(m.Config.Logging) {
						c.warn(src.at("config.logging.payloads"), ConfigFieldOverridden,
							"model %q sets config.logging.payloads to true. "+
								"This will be ignored on the %q route, and log_payloads will be "+
								"overridden to false because the data plane rejects it on realtime routes.",
							m.Name, g.route.Name)
					}
					c.warnTransportOverrides(src, m, g.route)
				} else {
					c.warnStatisticsOverride(src, m, g.route.Name, statsSupported, statsWarned)
				}

				pg := g.proxyByOwner[ownerKey]
				if pg != nil && !modelScoped {
					// type:"api" models on one route share a single route-scoped
					// ai-proxy-advanced, so any datastore mismatch would either drop
					// one model's datastore or make another silently inherit it.
					if (ds == nil) != (pg.datastoreName == "") ||
						(ds != nil && ds.Name != pg.datastoreName) {
						current := "no datastore"
						if ds != nil {
							current = fmt.Sprintf("datastore %q", ds.Name)
						}
						existing := "no datastore"
						if pg.datastoreName != "" {
							existing = fmt.Sprintf("datastore %q", pg.datastoreName)
						}
						return c.fail(src.at("datastores"), ConfigFieldInvalid,
							"model %q cannot share route %q with another api model: shared "+
								"route-scoped ai-proxy-advanced would mix %s and %s",
							m.Name, g.route.Name, existing, current)
					}
					if !reflect.DeepEqual(pg.tokenVault, tokenVault) {
						return c.fail(src.at("targets"), ConfigFieldInvalid,
							"model %q cannot share route %q with another api model that uses "+
								"a different provider token_vault", m.Name, g.route.Name)
					}
				}
				if pg == nil {
					embeddings, err := c.resolveEmbeddings(src, balancerExtra(m.Config.Balancer, "embeddings"))
					if err != nil {
						return err
					}
					vectordb, err := c.modelVectorDB(src, m, ds)
					if err != nil {
						return err
					}
					datastoreName := ""
					if ds != nil {
						datastoreName = ds.Name
					}
					// Each plugin's model FK must equal an ai_models.name — the string a
					// client sends, which ai-model-selector matches on to activate this
					// model-scoped plugin. Those identities are aliases (the authored
					// aliases, or the model name when none is set), not m.Name; using
					// m.Name would dangle the FK whenever an alias is authored.
					var pluginAliases []string
					realtimeModel := ""
					switch {
					case fkScoped && websocket:
						realtimeModel = aliases[0]
					case fkScoped:
						pluginAliases = aliases
					}

					modelNameHeader := boolPtr(false)
					if aimap.SupportsModelNameHeader(spec) {
						modelNameHeader = m.Config.Model.NameHeader
					}

					pg = &proxyGroup{
						routeName:         g.route.Name,
						aliases:           pluginAliases,
						realtimeModel:     realtimeModel,
						llmFormat:         format,
						genaiCategory:     spec.GenaiCategory,
						balancer:          c.balancerConfig(m.Config.Balancer),
						vectordb:          vectordb,
						datastoreName:     datastoreName,
						tokenVault:        tokenVault,
						embeddings:        embeddings,
						responseStreaming: m.Config.ResponseStreaming,
						modelNameHeader:   modelNameHeader,
						maxBodySize:       m.Config.MaxRequestBodySize,
						proxy:             proxyConfigBlock(m.Config.Proxy),
						source: source("model", m.Name, "config",
							kong.FieldMapping{GeneratedPrefix: "config.proxy_config", SourcePrefix: "config.proxy"},
							kong.FieldMapping{GeneratedPrefix: "config.model_name_header", SourcePrefix: "config.model.name_header"},
							kong.FieldMapping{GeneratedPrefix: "config.vectordb", SourcePrefix: "config.balancer.vectordb"},
							kong.FieldMapping{GeneratedPrefix: "config.embeddings", SourcePrefix: "config.balancer.embeddings"},
						),
						seen: map[string]int{},
					}
					g.proxyByOwner[ownerKey] = pg
					g.proxies = append(g.proxies, pg)
				} else if pg.genaiCategory != spec.GenaiCategory {
					// The data plane requires every target to match the plugin's category.
					if _, ok := collapsed[capability]; !ok {
						collapsed[capability] = collision{spec.GenaiCategory, g.route.Name, pg.genaiCategory}
						collapsedOrder = append(collapsedOrder, capability)
					}
					continue
				}
				routed[capability] = true
				// A target's model_alias decides which balancer pool it joins: the DP
				// keys pools by model_alias and puts alias-less targets in the shared
				// "<default>" pool, routing a request to an alias pool only when the
				// request presents that alias (its body model, or the model segment of
				// a native URL). type:model requests always carry the model, so their
				// targets must be aliased or a request lands in an empty "<default>"
				// pool (404 "no model matched this request"). type:api (files/batches)
				// requests carry no model, so their targets must stay alias-less to
				// remain in the "<default>" pool they fall back to (else the balancer
				// has no pool for the request: 500 "failed to get balancer instance").
				// The alias itself is baked in per plugin copy at emission time (see
				// withModelAlias), since a type:model target list is cloned once per
				// alias.
				// A passthrough target carries no model_alias either (the plugin refuses
				// it): fkScoped leaves pluginAliases empty, so emission never stamps one.
				target := c.buildTarget(tm, provider, providerType, spec.RouteType, logging)
				setEndpointUpstreamURL(target, sec, spec, tm)
				// Dedup on the built target's full shape rather than
				// (name, route_type): two targets can share a model name yet
				// be genuinely distinct via different providers (distinct
				// auth/options) or weights. The same (tm, provider) reached
				// through several capabilities that share a route_type still
				// fingerprints identically, so those collapse as before —
				// but every capability that produced the collapsed target is
				// still recorded (e.g. bedrock's shared invoke endpoint for
				// "generate" and "audio/speech"), rather than only the first.
				dedup := targetFingerprint(target)
				if idx, ok := pg.seen[dedup]; ok {
					source := &pg.targetSources[idx]
					if capability != "" && !slices.Contains(source.Capabilities, capability) {
						source.Capabilities = append(source.Capabilities, capability)
					}
				} else {
					var sourceCaps []string
					if capability != "" {
						sourceCaps = []string{capability}
					}
					pg.seen[dedup] = len(pg.targets)
					pg.targets = append(pg.targets, target)
					pg.targetSources = append(pg.targetSources, kong.TargetSource{
						ModelName:        m.Name,
						ModelTargetIndex: j,
						Capabilities:     sourceCaps,
					})
				}
			}
		}
	}
	// A capability that another endpoint still serves loses the collided
	// endpoint with no issue.
	for _, capability := range collapsedOrder {
		if routed[capability] || lists.capsWildcard {
			continue
		}
		col := collapsed[capability]
		c.warn(src.at(capabilityField(m, capability)), ConfigFieldDropped,
			"model %q: capability %q (genai_category %q) collapses onto route %q, which already "+
				"serves genai_category %q; a single ai-proxy-advanced cannot serve multiple genai "+
				"categories, skipping this capability",
			m.Name, capability, col.category, col.route, col.existing)
	}

	st.tokenVaults[m] = tokenVault

	c.writeAIModels(st, src, m, aliases)

	// Model policy and ACL plugins scope to each route the model produces, plus
	// the ai-model entity for type "model".
	if len(m.TargetModels) > 0 && len(targets) == 0 {
		return c.fail(src.at("targets"), MissingRequiredConfigField,
			"model %q has no target that the converter can convert", m.Name)
	}
	if !passthrough {
		if err := c.checkTargetSupport(src, m, lists, formats, servedBy, targets); err != nil {
			return err
		}
		c.warnUnservedFormats(src, m, lists, supportedPairs)
	}

	plugins, err := c.scopedPlugins(src, m.Policies, m.Access.ACLs)
	if err != nil {
		return err
	}
	plugins = sourceScopedPlugins(plugins, "model", m.Name)
	for _, routeName := range routeNames {
		for k := range plugins {
			p := plugins[k]
			p.Route = kong.NewStringRef(routeName)
			if protocols, ok := st.websocketRoutes[routeName]; ok {
				if !aimap.SupportsWebSocket(p.Name) {
					c.warn(src.at(scopedPluginField(m.Policies, p)), ConfigFieldDropped,
						"model %q uses plugin %q, which does not support the ws and wss protocols. "+
							"This plugin will be ignored on the WebSocket route %q.",
						m.Name, p.Name, routeName)
					continue
				}
				p.Protocols = protocols
				st.guardPlugins = append(st.guardPlugins, p)
				continue
			}
			if !fkScoped {
				st.guardPlugins = append(st.guardPlugins, p)
				continue
			}
			// Kong cannot read the ordering of a model-scoped plugin before
			// ai-model-selector sets the model. The route's selector carries
			// the ordering instead. See "Assumptions and limitations" in README.md.
			if aimap.OutranksModelSelector(p.Name) {
				if !slices.Contains(st.selectorBefore[routeName], p.Name) {
					st.selectorBefore[routeName] = append(st.selectorBefore[routeName], p.Name)
				}
			} else if priority, ok := c.customPolicyPriority(p.Name); ok && priority > aimap.AIModelSelectorPriority {
				// aimap.PluginPriority has no entry for a custom plugin, so the
				// converter cannot add it to the route's ai-model-selector
				// ordering the way it does for a known high-priority plugin.
				c.warn(src.at(scopedPluginField(m.Policies, p)), BehaviorMayDiffer,
					"model %q: custom policy %q sets PRIORITY %d, which outranks ai-model-selector (%d). "+
						"The converter cannot reorder a custom plugin, so this model-scoped instance "+
						"on route %q never runs. Give the policy a lower PRIORITY for it to run for this "+
						"model.",
					m.Name, p.Name, priority, aimap.AIModelSelectorPriority, routeName)
			}
			if p.Name == "request-termination" {
				ref := modelPolicyRef{model: m.Name, field: scopedPluginField(m.Policies, p)}
				if !slices.Contains(st.requestTerminations[routeName], ref) {
					st.requestTerminations[routeName] = append(st.requestTerminations[routeName], ref)
				}
			}
			// Same ai-model identities as the ai-proxy-advanced FKs: one copy
			// of this policy/ACL plugin per alias, so a request is protected
			// regardless of which alias it names.
			for _, alias := range aliases {
				pCopy := p
				pCopy.Model = kong.NewStringRef(alias)
				st.guardPlugins = append(st.guardPlugins, pCopy)
			}
		}
	}

	// Each route group contains only models with the same auth-strategy
	// set, so these plugins can safely remain route-scoped.
	idpPlugins, err := c.scopedAuthStrategyPlugins(src, m.Access.AuthStrategies)
	if err != nil {
		return err
	}
	for _, routeName := range routeNames {
		key := routeName + "\x00" + authStrategyKey(m.Access.AuthStrategies)
		if st.identityPluginSeen[key] || len(idpPlugins) == 0 {
			continue
		}
		st.identityPluginSeen[key] = true
		protocols, websocket := st.websocketRoutes[routeName]
		if websocket && len(idpPlugins) > 1 {
			return c.fail(src.at("access.auth_strategies"), ConfigFieldInvalid,
				"model %q: the WebSocket route %q supports only one auth strategy, because "+
					"request-termination cannot reject the anonymous consumer on ws and wss routes",
				m.Name, routeName)
		}
		fallback := needsAnonymousFallback(idpPlugins)
		if fallback {
			c.ensureAnonymousConsumer()
		}
		for k := range idpPlugins {
			p := idpPlugins[k]
			p.Route = kong.NewStringRef(routeName)
			if !fallback {
				p = withoutAnonymousFallback(p)
			}
			if websocket {
				p.Protocols = protocols
			} else if !slices.Contains(st.selectorAfter[routeName], p.Name) {
				st.selectorAfter[routeName] = append(st.selectorAfter[routeName], p.Name)
			}
			st.guardPlugins = append(st.guardPlugins, p)
		}
		if fallback && !slices.Contains(st.selectorAfter[routeName], "request-termination") {
			// Auth plugins fall back to the anonymous consumer. Its
			// request-termination rejects the request.
			st.selectorAfter[routeName] = append(st.selectorAfter[routeName], "request-termination")
		}
	}
	return nil
}

// selectorOrdering returns the ordering of the ai-model-selector on
// routeName. It is nil unless a model policy outranks the selector.
func (c *Converter) selectorOrdering(st *modelPass, routeName string) *kong.Ordering {
	before := st.selectorBefore[routeName]
	if len(before) == 0 {
		return nil
	}
	ordering := &kong.Ordering{Before: &kong.OrderingPhases{Access: before}}
	// Reordering must not move authentication after the selector or the
	// policies. Kong rejects unauthenticated requests before the selector
	// reads the body.
	if after := st.selectorAfter[routeName]; len(after) > 0 {
		ordering.After = &kong.OrderingPhases{Access: after}
		if slices.Contains(after, "request-termination") {
			c.warnModelScopedRequestTermination(routeName, st.requestTerminations[routeName])
		}
	}
	return ordering
}

// warnModelScopedRequestTermination warns once per policy reference in refs.
// The selector's ordering.after runs every request-termination before the
// model is known, so these policies never match.
func (c *Converter) warnModelScopedRequestTermination(routeName string, refs []modelPolicyRef) {
	for _, ref := range refs {
		c.warn(issueSource(kindModel, ref.model).at(ref.field), BehaviorMayDiffer,
			"route %q: ordering moves request-termination before ai-model-selector. "+
				"A model-scoped request-termination policy on this route never runs. "+
				"Scope the policy to the route or globally instead.", routeName)
	}
}

// convertVideoLifecycle adds the video lifecycle route of candidate to service.
func (c *Converter) convertVideoLifecycle(
	st *modelPass, candidate videoLifecycleCandidate, service *kong.Service,
) error {
	src := issueSource(kindModel, candidate.model.Name)
	routeName := uniqueModelRouteName("openai-videos-lifecycle", st.usedRouteNames)
	route := buildVideoLifecycleRoute(candidate.model.Config.Route, routeName, basePaths(candidate.model))
	route.Source = source("model", candidate.model.Name, "config.route")
	service.Routes = append(service.Routes, route)

	targets := make([]map[string]any, 0, len(candidate.targets))
	targetSources := make([]kong.TargetSource, 0, len(candidate.targets))
	for targetIndex, lifecycleTarget := range candidate.targets {
		logging := modelLoggingBlock(
			withLoggingDefaults(candidate.model.Config.Logging, false, false),
			aimap.LogStatisticsSupported(lifecycleTarget.providerType, candidate.spec), false,
		)
		targets = append(targets, c.buildTarget(
			lifecycleTarget.target,
			lifecycleTarget.provider,
			lifecycleTarget.providerType,
			candidate.spec.RouteType,
			logging,
		))
		targetSources = append(targetSources, kong.TargetSource{
			ModelName:        candidate.model.Name,
			ModelTargetIndex: targetIndex,
			Capabilities:     []string{"video"},
		})
	}
	lifecycleDS, err := c.resolveModelDatastore(src, candidate.model)
	if err != nil {
		return err
	}
	lifecycleVectorDB, err := c.modelVectorDB(src, candidate.model, lifecycleDS)
	if err != nil {
		return err
	}
	pg := &proxyGroup{
		routeName:         routeName,
		llmFormat:         "openai",
		genaiCategory:     candidate.spec.GenaiCategory,
		balancer:          c.balancerConfig(candidate.model.Config.Balancer),
		vectordb:          lifecycleVectorDB,
		tokenVault:        st.tokenVaults[candidate.model],
		responseStreaming: candidate.model.Config.ResponseStreaming,
		modelNameHeader:   boolPtr(false),
		maxBodySize:       candidate.model.Config.MaxRequestBodySize,
		proxy:             proxyConfigBlock(candidate.model.Config.Proxy),
		targets:           targets,
		targetSources:     targetSources,
		source: source("model", candidate.model.Name, "config",
			kong.FieldMapping{GeneratedPrefix: "config.proxy_config", SourcePrefix: "config.proxy"},
			kong.FieldMapping{GeneratedPrefix: "config.model_name_header", SourcePrefix: "config.model.name_header"},
			kong.FieldMapping{GeneratedPrefix: "config.vectordb", SourcePrefix: "config.balancer.vectordb"},
			kong.FieldMapping{GeneratedPrefix: "config.embeddings", SourcePrefix: "config.balancer.embeddings"},
		),
	}
	c.out.Plugins = append(c.out.Plugins, kong.Plugin{
		Name:          "ai-proxy-advanced",
		Route:         kong.NewStringRef(routeName),
		Config:        pg.proxyConfig(),
		TargetSources: pg.targetSources,
		Source:        pg.source,
	})
	c.appendTokenVaultExchange(pg, nil)

	plugins, err := c.scopedPlugins(src, candidate.model.Policies, candidate.model.Access.ACLs)
	if err != nil {
		return err
	}
	plugins = sourceScopedPlugins(plugins, "model", candidate.model.Name)
	for i := range plugins {
		plugins[i].Route = kong.NewStringRef(routeName)
		c.out.Plugins = append(c.out.Plugins, plugins[i])
	}
	idpPlugins, err := c.scopedAuthStrategyPlugins(src, candidate.model.Access.AuthStrategies)
	if err != nil {
		return err
	}
	fallback := needsAnonymousFallback(idpPlugins)
	if fallback {
		c.ensureAnonymousConsumer()
	}
	for i := range idpPlugins {
		if !fallback {
			idpPlugins[i] = withoutAnonymousFallback(idpPlugins[i])
		}
		idpPlugins[i].Route = kong.NewStringRef(routeName)
		c.out.Plugins = append(c.out.Plugins, idpPlugins[i])
	}
	return nil
}

// needsAnonymousFallback reports whether the auth plugins of a model route
// need the anonymous consumer. Only more than one plugin needs it, so that a
// request passes when any one of them accepts it. One plugin rejects the
// request itself. Then request-termination stays out of the selector
// ordering, and a model-scoped request-termination policy can run.
func needsAnonymousFallback(idpPlugins []kong.Plugin) bool {
	return len(idpPlugins) > 1
}

// authStrategyKey canonicalizes auth-strategy references for route
// grouping. Duplicate references have no semantic effect, and their ordering
// must not make otherwise identical access policies create separate routes.
func authStrategyKey(refs []string) string {
	seen := make(map[string]bool, len(refs))
	var unique []string
	for _, ref := range refs {
		if !seen[ref] {
			seen[ref] = true
			unique = append(unique, ref)
		}
	}
	sort.Strings(unique)
	return strings.Join(unique, "\x00")
}

// videoLifecycleCandidates returns video routes that can serve requests with no
// model alias. Multiple targets are retained and reported as a warning: the
// route remains usable, but video_id alone cannot identify the credentials that
// created the job. Shared model routes are also retained, with a warning, so
// users with distinct route matchers still receive every lifecycle route.
// Models in skipped and targets that checkTarget rejects are left out.
func (c *Converter) videoLifecycleCandidates(skipped map[int][]ConversionIssue) []videoLifecycleCandidate {
	var candidates []videoLifecycleCandidate
	sharedRoutes := map[string][]int{}
	var sharedOrder []string
	// explicitVideo holds the models that list video. A video capability
	// from "*" gives no lifecycle warning.
	explicitVideo := map[int]bool{}

	for i := range c.src.Models {
		m := &c.src.Models[i]
		if _, ok := skipped[i]; ok || isDisabled(m) {
			continue
		}
		// A passthrough model's base-path route already forwards the lifecycle
		// requests; it declares no video endpoint of its own.
		if isPassthrough(m) {
			continue
		}
		wildcard := capabilitiesWildcard(m)
		explicitVideo[i] = !wildcard && slices.Contains(explicitCapabilities(m), "video")
		if !explicitVideo[i] && (!wildcard || !isModelType(m)) {
			continue
		}
		src := issueSource(kindModel, m.Name)
		if len(m.TargetModels) == 0 {
			if explicitVideo[i] {
				c.warn(src.at("targets"), BehaviorMayDiffer,
					"model %q: video lifecycle routes require at least one target; skipping them", m.Name)
			}
			continue
		}

		var planned []plannedTarget
		for j := range m.TargetModels {
			provider, providerType, problem := c.checkTarget(m, j)
			if problem != nil {
				continue
			}
			planned = append(planned, plannedTarget{
				index: j, tm: &m.TargetModels[j], provider: provider, providerType: providerType,
				target: aimapTarget(&m.TargetModels[j], providerType),
			})
		}
		lists := modelLists{formats: explicitFormats(m), formatsWildcard: formatsWildcard(m)}
		section := "openai"
		if !slices.Contains(c.effectiveFormats(lists, planned), section) {
			continue
		}
		spec, ok := aimap.LookupEndpoint(section, "video")
		if !ok {
			continue
		}
		lifecycleTargets := make([]videoLifecycleTarget, 0, len(m.TargetModels))
		for _, t := range planned {
			if !aimap.TargetSupports(section, t.target, spec) {
				continue
			}
			lifecycleTargets = append(lifecycleTargets, videoLifecycleTarget{
				target: t.tm, provider: t.provider, providerType: t.providerType,
			})
		}
		// convertModel fails the model when no target is left.
		if len(lifecycleTargets) == 0 {
			continue
		}
		if len(lifecycleTargets) > 1 && explicitVideo[i] {
			c.warn(src.at("targets"), BehaviorMayDiffer,
				"model %q: video lifecycle route has multiple targets; "+
					"requests without a model alias may reach a different target than creation", m.Name)
		}

		routeConfig := m.Config.Route
		routeConfig.Name = ""
		routeConfig.Methods = nil
		key, err := modelRouteConfigKey(routeConfig)
		if err != nil {
			key = m.Name
		}
		key = section + "|" + key
		if _, ok := sharedRoutes[key]; !ok {
			sharedOrder = append(sharedOrder, key)
		}
		sharedRoutes[key] = append(sharedRoutes[key], i)
		candidates = append(candidates, videoLifecycleCandidate{
			index: i, model: m, targets: lifecycleTargets, spec: spec,
		})
	}

	for _, key := range sharedOrder {
		indexes := sharedRoutes[key]
		if len(indexes) < 2 { //nolint:mnd
			continue
		}
		names := make([]string, len(indexes))
		for k, i := range indexes {
			names[k] = c.src.Models[i].Name
		}
		for _, i := range indexes {
			if !explicitVideo[i] {
				continue
			}
			c.warn(issueSource(kindModel, c.src.Models[i].Name).at("config.route"), RoutingConflict,
				"video lifecycle routes for models %s: route is shared by multiple "+
					"video models; emitting overlapping lifecycle routes", strings.Join(names, ", "))
		}
	}
	return candidates
}

// targetProblem tells why checkTarget leaves out a target.
type targetProblem struct {
	field  string
	code   ConversionIssueCode
	reason string
}

// checkTarget resolves the provider of target j of m. It returns a problem when
// the converter must leave out the target.
func (c *Converter) checkTarget(m *aigw.Model, j int) (*aigw.Provider, string, *targetProblem) {
	tm := &m.TargetModels[j]
	provider := c.providers[tm.Provider]
	if tm.Provider == "" {
		return nil, "", &targetProblem{
			field:  fmt.Sprintf("targets[%d].provider", j),
			code:   MissingRequiredConfigField,
			reason: fmt.Sprintf("model %q target %q has no provider; the target is omitted", m.Name, tm.Name),
		}
	}
	if provider == nil {
		return nil, "", &targetProblem{
			field: fmt.Sprintf("targets[%d].provider", j),
			code:  UndefinedReference,
			reason: fmt.Sprintf("model %q target %q references unknown provider %q; the target is omitted",
				m.Name, tm.Name, tm.Provider),
		}
	}
	providerType := tm.Config.Type
	if providerType == "" {
		providerType = provider.Type
	}
	if providerType == "" {
		return nil, "", &targetProblem{
			field:  fmt.Sprintf("targets[%d].config.type", j),
			code:   MissingRequiredConfigField,
			reason: fmt.Sprintf("model %q target %q has no resolvable provider type; the target is omitted", m.Name, tm.Name),
		}
	}
	if f, gated := aimap.ProviderFeature(providerType); gated && !c.supports(f) {
		field := fmt.Sprintf("targets[%d].config.type", j)
		if tm.Config.Type == "" {
			field = fmt.Sprintf("targets[%d].provider", j)
		}
		return nil, "", &targetProblem{
			field: field,
			code:  UnsupportedInMinRuntimeVersion,
			reason: unsupportedReason(f, "provider type "+providerType, c.minRuntimeVersion) +
				"; the target is omitted",
		}
	}
	// Databricks has no fixed host: the plugin builds it from
	// workspace_instance_id only when it builds the request, which
	// passthrough never does, so the upstream must be spelled out.
	if isPassthrough(m) && providerType == "databricks" && tm.Config.Options["upstream_url"] == nil {
		return nil, "", &targetProblem{
			field: fmt.Sprintf("targets[%d].config.upstream_url", j),
			code:  MissingRequiredConfigField,
			reason: fmt.Sprintf("model %q target %q: the passthrough format requires upstream_url for "+
				"databricks; the target is omitted", m.Name, tm.Name),
		}
	}
	// The Mantle endpoint does not serve the Converse or InvokeModel APIs.
	// Under "*", aimap.SupportedFormats always selects openai for it.
	if formats := clientFormats(explicitFormats(m), providerType); isMantleTarget(tm, providerType) &&
		formats != nil && !slices.Contains(formats, "openai") && !slices.Contains(formats, "anthropic") {
		return nil, "", &targetProblem{
			field: fmt.Sprintf("targets[%d].config.endpoint_type", j),
			code:  ConfigFieldInvalid,
			reason: fmt.Sprintf("model %q target %q: the mantle endpoint type is not supported with the "+
				"%s format; the target is omitted", m.Name, tm.Name, strings.Join(formats, ", ")),
		}
	}
	if providerType == "gemini" {
		env := aimap.GeminiEnvFromOptions(tm.Config.Options)
		if key := env.InvalidField(); key != "" {
			return nil, "", &targetProblem{
				field: fmt.Sprintf("targets[%d].config.gcp_environment.%s", j, key),
				code:  ConfigFieldInvalid,
				reason: fmt.Sprintf("model %q target %q: gcp_environment.%s is not a valid Google Cloud ID; "+
					"the target is omitted", m.Name, tm.Name, key),
			}
		}
	}
	return provider, providerType, nil
}

// clientFormats returns the llm_format of each format for providerType.
func clientFormats(formats []string, providerType string) []string {
	if formats == nil {
		return nil
	}
	out := make([]string, len(formats))
	for i, format := range formats {
		out[i] = aimap.ClientFormat(format, providerType)
	}
	return out
}

func isMantleTarget(tm *aigw.TargetModel, providerType string) bool {
	return providerType == "bedrock" && tm.Config.Options["endpoint_type"] == aimap.BedrockEndpointTypeMantle
}

// checkTargetSupport fails m when no target serves an explicit capability.
// It warns for each target that cannot serve a capability that another
// target serves. served holds the indexes of the targets that serve each
// capability.
func (c *Converter) checkTargetSupport(
	src ConversionIssueSource, m *aigw.Model, lists modelLists, formats []string,
	served map[string][]int, targets []plannedTarget,
) error {
	if len(targets) == 0 || lists.capsWildcard {
		return nil
	}
	format := strings.Join(formats, ", ")
	for _, capability := range lists.capabilities {
		field := capabilityField(m, capability)
		if len(served[capability]) == 0 {
			return c.fail(src.at(field), ConfigFieldInvalid,
				"model %q: no target can serve capability %q with llm_format %q",
				m.Name, capability, format)
		}
		for _, t := range targets {
			if slices.Contains(served[capability], t.index) {
				continue
			}
			c.warn(src.at(field, fmt.Sprintf("targets[%d]", t.index)), BehaviorMayDiffer,
				"model %q target %q: the data plane does not accept this target for capability %q "+
					"with llm_format %q. The routes of this capability will not fail over to it.",
				m.Name, t.tm.Name, capability, format)
		}
	}
	return nil
}

// capabilityField returns the path of the entry of m.Capabilities that
// expands to capability.
func capabilityField(m *aigw.Model, capability string) string {
	for i, raw := range m.Capabilities {
		if slices.Contains(aimap.NormalizeCapability(raw), capability) {
			return fmt.Sprintf("capabilities[%d]", i)
		}
	}
	return "capabilities"
}

// scopedPluginField returns the path of the source of a plugin that
// scopedPlugins built from refs.
func scopedPluginField(refs []string, p kong.Plugin) string {
	if p.Source != nil && p.Source.EntityType == "policy" {
		if i := slices.Index(refs, p.Source.EntityName); i >= 0 {
			return fmt.Sprintf("policies[%d]", i)
		}
	}
	return "access.acls"
}

func buildVideoLifecycleRoute(rc aigw.ModelRouteConfig, routeName string, bases []string) kong.Route {
	rc.Methods = nil
	paths := make([]string, 0, len(bases))
	for _, base := range bases {
		base = strings.TrimRight(base, "/")
		paths = append(paths, base+"/videos")
	}
	route := buildModelRoute(rc, routeName, paths, []string{"GET", "DELETE"}, false)
	route.Tags = append(route.Tags, aimap.VideoLifecycleRouteTag)
	return route
}

// warnTransportOverrides reports route methods and protocols of m that the
// transport of route replaces.
func (c *Converter) warnTransportOverrides(src ConversionIssueSource, m *aigw.Model, route kong.Route) {
	rc := m.Config.Route
	if len(rc.Methods) > 0 && len(route.Methods) == 0 {
		c.warn(src.at("config.route.methods"), ConfigFieldIgnored,
			"model %q sets config.route.methods. This will be ignored on the WebSocket route %q, "+
				"because Kong does not match methods on ws and wss routes.",
			m.Name, route.Name)
	}
	if len(rc.Protocols) > 0 && !slices.Equal(rc.Protocols, route.Protocols) {
		c.warn(src.at("config.route.protocols"), ConfigFieldOverridden,
			"model %q sets config.route.protocols to %v. This will be overridden to %v on the %q route, "+
				"because the route transport supports only these protocols.",
			m.Name, rc.Protocols, route.Protocols, route.Name)
	}
}

// modelRouteConfigKey returns a stable representation of the client-facing
// route configuration. Endpoint paths are derived separately, but the base
// paths and every other route matcher must agree before models can share a
// route. The path alias is per-model, not per-route, so it is excluded:
// models with distinct aliases still share a route (and its ai-model-selector)
// when every other matcher agrees.
func modelRouteConfigKey(route aigw.ModelRouteConfig) (string, error) {
	route.Model = aigw.ModelSelectorConfig{}
	b, err := json.Marshal(route)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// passthroughMatcherKey identifies the requests a passthrough route on base path b
// matches: only the Kong matcher fields, so routes differing in name, tags or
// buffering still collide while hosts/headers/... disambiguate them.
// ponytail: exact equality, so partially overlapping hosts/methods are not caught.
func passthroughMatcherKey(b string, route aigw.ModelRouteConfig) (string, error) {
	k, err := json.Marshal(aigw.ModelRouteConfig{
		Paths:        []string{b},
		Hosts:        route.Hosts,
		Methods:      route.Methods,
		Protocols:    route.Protocols,
		Headers:      route.Headers,
		SNIs:         route.SNIs,
		Sources:      route.Sources,
		Destinations: route.Destinations,
	})
	return string(k), err
}

// uniqueModelRouteName reserves base if available, otherwise returns a stable
// numeric suffix for another route serving the same endpoint.
func uniqueModelRouteName(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for n := 2; ; n++ {
		name := base + "-" + strconv.Itoa(n)
		if !used[name] {
			used[name] = true
			return name
		}
	}
}

// proxyConfig assembles the ai-proxy-advanced plugin config for a proxy group.
func (g *proxyGroup) proxyConfig() map[string]any {
	cfg := map[string]any{
		"balancer":       g.balancer,
		"llm_format":     g.llmFormat,
		"genai_category": g.genaiCategory,
		"targets":        g.targets,
	}
	if g.vectordb != nil {
		cfg["vectordb"] = g.vectordb
	}
	if g.embeddings != nil {
		cfg["embeddings"] = g.embeddings
	}
	if g.responseStreaming != "" {
		cfg["response_streaming"] = g.responseStreaming
	}
	if g.modelNameHeader != nil {
		cfg["model_name_header"] = *g.modelNameHeader
	}
	if g.maxBodySize != nil {
		cfg["max_request_body_size"] = *g.maxBodySize
	}
	if g.proxy != nil {
		cfg["proxy_config"] = g.proxy
	}
	return cfg
}

func loggingStatisticsSet(l *aigw.Logging) bool {
	return l != nil && l.Statistics != nil && *l.Statistics
}

func loggingPayloadsSet(l *aigw.Logging) bool {
	return l != nil && l.Payloads != nil && *l.Payloads
}

// warnStatisticsOverride warns one time for each route when a target of m
// gets log_statistics false although m sets statistics to true.
func (c *Converter) warnStatisticsOverride(
	src ConversionIssueSource, m *aigw.Model, routeName string, supported bool, warned map[string]bool,
) {
	if supported || warned[routeName] || !loggingStatisticsSet(m.Config.Logging) {
		return
	}
	warned[routeName] = true
	c.warn(src.at("config.logging.statistics"), ConfigFieldOverridden,
		"model %q sets config.logging.statistics to true. "+
			"This will be ignored on the %q route, and log_statistics will be "+
			"overridden to false because the endpoint does not support it.",
		m.Name, routeName)
}

// modelLoggingBlock maps a model's AI Gateway logging into the per-target logging
// record accepted by the ai-proxy-advanced target schema, which only allows
// log_statistics and log_payloads. Any extra keys loggingBlock may produce
// (max_payload_size, log_audits) are dropped to avoid emitting unknown fields.
// The data plane rejects log_payloads true on a realtime target.
func modelLoggingBlock(l *aigw.Logging, supportsLogStatistics, realtime bool) map[string]any {
	block := loggingBlock(l)
	if block == nil {
		return nil
	}

	if !supportsLogStatistics {
		block["log_statistics"] = false
	}
	if realtime {
		block["log_payloads"] = false
	}
	delete(block, "max_payload_size")
	delete(block, "log_audits")
	if len(block) == 0 {
		return nil
	}
	return block
}

// buildTarget builds one ai-proxy-advanced target from a target model. The
// model-level logging block (if any) is applied to every target, since
// ai-proxy-advanced carries logging per target rather than per plugin. The
// target's model_alias is not set here: a multi-alias model's target list is
// cloned once per alias at emission time (see withModelAlias).
func (c *Converter) buildTarget(
	tm *aigw.TargetModel, provider *aigw.Provider,
	providerType, routeType string, logging map[string]any,
) map[string]any {
	model := map[string]any{
		"provider": aimap.PluginProvider(providerType),
		"name":     tm.Name,
	}
	passthrough := routeType == aimap.PassthroughRouteType
	opts := mapOptions(c.supportedTargetOptions(tm.Config.Options), providerType, tm.Name, provider, passthrough)
	if opts != nil {
		model["options"] = opts
	}

	target := map[string]any{
		"route_type": routeType,
		"model":      model,
	}
	if auth := resolveAuth(provider, tm.AllowAuthOverride); auth != nil {
		target["auth"] = auth
	}
	if tm.Weight != nil {
		target["weight"] = *tm.Weight
	}
	if tm.SemanticDesc != "" {
		target["description"] = tm.SemanticDesc
	} else {
		target["description"] = tm.Name // Use name as default description.
	}
	if logging != nil {
		target["logging"] = logging
	}
	return target
}

// withModelAlias clones each target (and its nested "model" map) with
// model_alias set to alias, so the N ai-proxy-advanced plugins emitted for a
// multi-alias model don't share mutable map state. A Gemini Interactions
// target keeps no model_alias.
func withModelAlias(targets []map[string]any, alias, llmFormat string) []map[string]any {
	out := make([]map[string]any, len(targets))
	for i, t := range targets {
		model, _ := t["model"].(map[string]any)
		provider, _ := model["provider"].(string)
		routeType, _ := t["route_type"].(string)
		if aimap.GeminiInteractions(llmFormat, provider, routeType) {
			out[i] = t
			continue
		}
		clone := make(map[string]any, len(t))
		maps.Copy(clone, t)
		modelClone := make(map[string]any, len(model)+1)
		maps.Copy(modelClone, model)
		modelClone["model_alias"] = alias
		clone["model"] = modelClone
		out[i] = clone
	}
	return out
}

// targetFingerprint returns a stable dedup key for a built ai-proxy-advanced
// target. json.Marshal sorts map keys, so structurally identical targets
// fingerprint identically regardless of build order. On the (unreachable in
// practice) marshal error the target name is used, which at worst keeps a
// duplicate rather than dropping a distinct target.
func targetFingerprint(target map[string]any) string {
	b, err := json.Marshal(target)
	if err != nil {
		return fmt.Sprintf("%v", target)
	}
	return string(b)
}

// checkModelFeatures fails m when the minimum runtime version lacks one of
// its formats. It warns for each gated failover_criteria value, which
// balancerConfig leaves out. checkModelCapabilities covers capabilities.
func (c *Converter) checkModelFeatures(src ConversionIssueSource, m *aigw.Model) error {
	for i, format := range m.Formats {
		if formatsWildcard(m) {
			break
		}
		if f, gated := aimap.FormatFeature(format.Type); gated && !c.supports(f) {
			field := fmt.Sprintf("formats[%d].type", i)
			return c.failUnsupported(src.at(field), f, fmt.Sprintf("%s: %s", field, format.Type))
		}
	}
	if b := m.Config.Balancer; b != nil && !c.supports(aimap.FeatureFailoverHTTPCodes) {
		criteria, _ := b.Fields["failover_criteria"].([]any)
		for i, v := range criteria {
			if code, _ := v.(string); aimap.FailoverHTTPCodes[code] {
				field := fmt.Sprintf("config.balancer.failover_criteria[%d]", i)
				c.reportUnsupported(src.at(field), Warning, aimap.FeatureFailoverHTTPCodes,
					fmt.Sprintf("%s: %s", field, code))
			}
		}
	}
	return nil
}

// checkBedrockEndpointType fails the model when target j sets a key of
// aimap.BedrockEndpointTypeKeys that the minimum runtime version lacks.
// Without the key, traffic goes to a different Bedrock endpoint.
func (c *Converter) checkBedrockEndpointType(src ConversionIssueSource, tm *aigw.TargetModel, j int) error {
	if c.supports(aimap.FeatureBedrockEndpointType) {
		return nil
	}
	for _, key := range aimap.BedrockEndpointTypeKeys {
		if _, ok := tm.Config.Options[key]; ok {
			field := fmt.Sprintf("targets[%d].config.%s", j, key)
			return c.failUnsupported(src.at(field), aimap.FeatureBedrockEndpointType, field)
		}
	}
	return nil
}

// warnUnsupportedCostLists warns for each cost list of target j that the
// minimum runtime version lacks. supportedTargetOptions leaves them out.
func (c *Converter) warnUnsupportedCostLists(src ConversionIssueSource, tm *aigw.TargetModel, j int) {
	if c.supports(aimap.FeatureTargetCostList) {
		return
	}
	for _, key := range aimap.TargetCostListKeys {
		if _, ok := tm.Config.Options[key]; ok {
			field := fmt.Sprintf("targets[%d].config.%s", j, key)
			c.reportUnsupported(src.at(field), Warning, aimap.FeatureTargetCostList, field)
		}
	}
}

// supportedTargetOptions returns opts without the keys that the minimum
// runtime version lacks. It does not change opts.
func (c *Converter) supportedTargetOptions(opts map[string]any) map[string]any {
	if c.supports(aimap.FeatureTargetCostList) {
		return opts
	}
	out := maps.Clone(opts)
	for _, key := range aimap.TargetCostListKeys {
		delete(out, key)
	}
	return out
}

// balancerHoisted are balancer-block fields that the plugin schema
// expects as siblings of `balancer`, not nested inside it.
var balancerHoisted = map[string]bool{"vectordb": true, "embeddings": true}

func (c *Converter) balancerConfig(b *aigw.Balancer) map[string]any {
	if b == nil {
		return map[string]any{"algorithm": "round-robin"}
	}
	cfg := map[string]any{}
	for k, v := range b.Fields {
		if balancerHoisted[k] {
			continue
		}
		cfg[k] = v
	}
	if criteria, ok := cfg["failover_criteria"].([]any); ok && !c.supports(aimap.FeatureFailoverHTTPCodes) {
		cfg["failover_criteria"] = slices.DeleteFunc(slices.Clone(criteria), func(v any) bool {
			code, _ := v.(string)
			return aimap.FailoverHTTPCodes[code]
		})
	}
	algorithm := b.Algorithm
	if algorithm == "" {
		algorithm = "round-robin"
	}
	cfg["algorithm"] = algorithm
	return cfg
}

// balancerExtra pulls a hoisted field (vectordb/embeddings) out of the balancer
// block so it can be emitted at the top level of the plugin config.
func balancerExtra(b *aigw.Balancer, key string) any {
	if b == nil {
		return nil
	}
	return b.Fields[key]
}

// resolveModelDatastore resolves m.Datastores (by-name references into the
// top-level datastores list). An unknown name gives an Error issue, and the
// model converts without the connection.
func (c *Converter) resolveModelDatastore(src ConversionIssueSource, m *aigw.Model) (*aigw.Datastore, error) {
	if len(m.Datastores) == 0 {
		return nil, nil
	}
	if len(m.Datastores) > 1 {
		return nil, c.fail(src.at("datastores"), ConfigFieldInvalid,
			"model %q has %d datastores, but only one is allowed",
			m.Name, len(m.Datastores))
	}
	if m.Config.Balancer == nil || m.Config.Balancer.Algorithm != "semantic" {
		return nil, c.fail(src.at("datastores", "config.balancer.algorithm"), ConfigFieldInvalid,
			"model %q references datastore %q, but datastores require the semantic balancer "+
				"(set config.balancer.algorithm: semantic)",
			m.Name, m.Datastores[0].Name)
	}
	ds := c.datastores[m.Datastores[0].Name]
	if ds == nil {
		c.reportError(src.at("datastores[0].name"), UndefinedReference,
			"model %q references unknown datastore %q", m.Name, m.Datastores[0].Name)
		return nil, nil
	}
	return ds, nil
}

// modelVectorDB lowers the model's balancer.vectordb to plugin shape, then
// injects the datastore's connection. The ordering is load-bearing: ds.Config
// already carries plugin-shaped keys (ssl_verify, sentinel_nodes, ...), so it
// is substituted after VectorDBToPlugin and lands verbatim in the strategy
// sub-block, replacing any inline connection ("the datastore wins") while the
// common fields the model authors itself are preserved.
func (c *Converter) modelVectorDB(src ConversionIssueSource, m *aigw.Model, ds *aigw.Datastore) (any, error) {
	vdb := balancerExtra(m.Config.Balancer, "vectordb")
	vectordb := aimap.VectorDBToPlugin(vdb)
	if ds == nil {
		return vectordb, nil
	}

	if vdb != nil {
		vectorDB, ok := vdb.(map[string]interface{})
		if !ok {
			return nil, c.fail(src.at("config.balancer.vectordb"), ConfigFieldInvalid,
				"model %q's balancer.vectordb is not a map", m.Name)
		}
		strategy, _ := aimap.VectorDBStrategyForDatastoreType(ds.Type)
		if vectorDB["strategy"] != strategy {
			return nil, c.fail(src.at("config.balancer.vectordb.strategy", "datastores[0].name"), ConfigFieldInvalid,
				"model %q's balancer.vectordb.strategy %q does not match "+
					"the datastore type %q (expected %q)",
				m.Name, vectorDB["strategy"], ds.Type, strategy)
		}
	}
	out, err := aimap.ApplyModelDatastore(vectordb, ds.Type, ds.Config)
	if err != nil {
		return nil, c.fail(src.at("datastores[0].name"), ConfigFieldInvalid,
			"model %q's datastore %q: %v", m.Name, ds.Name, err)
	}
	return out, nil
}

func basePaths(m *aigw.Model) []string {
	var out []string
	for _, p := range m.Config.Route.Paths {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{aimap.DefaultBasePath}
	}
	return out
}

// isDisabled reports whether m sets enabled to false.
func isDisabled(m *aigw.Model) bool { return m.Enabled != nil && !*m.Enabled }

// claimRouteAliases records the aliases of m on the route of g. It warns when
// another model on the route already uses one of them. The two models then
// make plugins with the same name, route, and ai_models scope.
// The data plane accepts this config, so both models stay.
func (c *Converter) claimRouteAliases(
	st *modelPass, src ConversionIssueSource, g *routeGroup, m *aigw.Model, aliases []string,
) {
	field := "name"
	if len(m.Config.Route.Model.Values) > 0 {
		field = "config.route.model.values"
	}
	if g.aliasOwners == nil {
		g.aliasOwners = map[string]string{}
	}
	for _, alias := range aliases {
		if other, ok := g.aliasOwners[alias]; ok {
			if key := m.Name + "\x00" + alias; !st.aliasConflicts[key] {
				st.aliasConflicts[key] = true
				c.warn(src.at(field), RoutingConflict,
					"model %q uses the alias %q on the route %q, but model %q already uses it there. "+
						"Both models make plugins with the same route and ai_models scope.",
					m.Name, alias, g.route.Name, other)
			}
			continue
		}
		g.aliasOwners[alias] = m.Name
	}
}

// writeAIModels writes one ai_models entry for each alias of m. The entry
// name is the alias. The entry alias stays unset for AI Gateway 2.0.0 data
// planes.
//
// Models on different routes can share an alias. The data plane rejects two
// entries with one name. A shared entry therefore keeps the values of the
// model that made it.
func (c *Converter) writeAIModels(st *modelPass, src ConversionIssueSource, m *aigw.Model, aliases []string) {
	var owned []string
	for _, alias := range aliases {
		if _, ok := st.aiModelRows[alias]; !ok {
			owned = append(owned, alias)
		}
	}
	// Revert merges only the entries with one group marker. It names the
	// merged model after the marker. m.Name does not round-trip.
	groupTag := ""
	if len(owned) > 1 {
		groupTag = aimap.EncodeModelAliasGroup(owned[0])
	}
	labelTags := c.labelsToTags(m.Labels)
	for _, alias := range aliases {
		if existing, ok := st.aiModelRows[alias]; ok {
			c.warnSharedAIModel(src, m, alias, labelTags, existing)
			continue
		}
		row := aiModelRow{owner: m.Name, labelTags: labelTags, index: len(c.out.AIModels)}
		if alias == owned[0] {
			row.id = m.ID
		}
		st.aiModelRows[alias] = row
		tags := slices.Clone(labelTags)
		if groupTag != "" {
			tags = append(tags, groupTag)
		}
		c.out.AIModels = append(c.out.AIModels, kong.AIModel{ID: row.id, Name: alias, Tags: tags})
	}
	if m.ID != "" && len(owned) == 0 {
		c.placeSharedID(st, src, m, aliases)
	}
}

// placeSharedID gives m.ID to the first shared entry of aliases without an
// ID. It warns when every entry already has an ID.
func (c *Converter) placeSharedID(st *modelPass, src ConversionIssueSource, m *aigw.Model, aliases []string) {
	for _, alias := range aliases {
		if row := st.aiModelRows[alias]; row.id == "" {
			row.id = m.ID
			st.aiModelRows[alias] = row
			c.out.AIModels[row.index].ID = m.ID
			return
		}
	}
	row := st.aiModelRows[aliases[0]]
	c.warn(src.at("id"), ConfigFieldDropped,
		"model %q shares the ai_models entry %q with model %q. The entry keeps the ID of model %q.",
		m.Name, aliases[0], row.owner, row.owner)
}

// aiModelRow holds what one model writes to an ai_models entry.
type aiModelRow struct {
	owner     string
	id        string
	labelTags []string
	// index is the position of the entry in c.out.AIModels.
	index int
}

// warnSharedAIModel warns for each value of m that the ai_models entry of
// alias cannot keep.
func (c *Converter) warnSharedAIModel(
	src ConversionIssueSource, m *aigw.Model, alias string, labelTags []string, got aiModelRow,
) {
	if len(labelTags) > 0 && !slices.Equal(labelTags, got.labelTags) {
		c.warn(src.at("labels"), ConfigFieldDropped,
			"model %q shares the ai_models entry %q with model %q. The entry keeps the labels of model %q.",
			m.Name, alias, got.owner, got.owner)
	}
	if len(m.Config.Route.Model.Values) > 1 {
		c.warn(src.at("config.route.model.values"), ConfigFieldDropped,
			"model %q shares the ai_models entry %q with model %q. "+
				"A revert gives %q its own model and does not restore the alias list of model %q.",
			m.Name, alias, got.owner, alias, m.Name)
	}
}

// checkSelectorConflict warns when the model named model reads the model
// name from other selector sources than a model already on g.
// A request can name a model in each source, so the source order decides.
func (c *Converter) checkSelectorConflict(
	st *modelPass, src ConversionIssueSource, g *routeGroup, model string, selectors []map[string]any,
) {
	shapes := selectorShapes(selectors)
	if len(shapes) == 0 {
		return
	}
	if g.selectorShapes == nil {
		g.selectorShapes = map[string][]string{}
	}
	g.selectorShapes[model] = shapes
	for _, other := range g.models {
		otherShapes := g.selectorShapes[other]
		if other == model || len(otherShapes) == 0 || slices.Equal(otherShapes, shapes) {
			continue
		}
		pair := other + "\x00" + model
		if st.selectorConflicts[pair] {
			continue
		}
		st.selectorConflicts[pair] = true
		c.warn(src.at("config.route.model"), RoutingConflict,
			"model %q and model %q share the route %q, but read the model name from different "+
				"sources. The order of the ai-model-selector sources decides the model for a "+
				"request that names a model in more than one source.",
			model, other, g.route.Name)
	}
}

// selectorShapes returns the sorted, unique keys of selectors without the
// max_request_body_size and pcre_pattern fields.
func selectorShapes(selectors []map[string]any) []string {
	var shapes []string
	for _, selector := range selectors {
		if selector == nil {
			continue
		}
		shape := maps.Clone(selector)
		delete(shape, "max_request_body_size")
		delete(shape, "pcre_pattern")
		key, err := json.Marshal(shape)
		if err != nil {
			continue
		}
		shapes = append(shapes, string(key))
	}
	slices.Sort(shapes)
	return slices.Compact(shapes)
}

// isModelType reports whether a model is a synchronous "model" entity (as
// opposed to an "api" entity for files/batches). An empty type defaults to
// "model", the discriminator default and the common synchronous case.
func isModelType(m *aigw.Model) bool { return m.Type != "api" }

// extractModelAliases returns the source model's authored aliases, or its name
// when none is authored. Values do not override the selector source. Always
// returns at least one alias. Duplicate values collapse to their first
// occurrence — Kong requires unique ai_models names, so a duplicated value
// would otherwise fan out into invalid, identically-named entities.
func (c *Converter) extractModelAliases(src ConversionIssueSource, m *aigw.Model) []string {
	values := m.Config.Route.Model.Values
	if len(values) == 0 {
		return []string{m.Name}
	}

	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	var dupes []string
	for _, v := range values {
		if seen[v] {
			if !slices.Contains(dupes, v) {
				dupes = append(dupes, v)
			}
			continue
		}
		seen[v] = true
		unique = append(unique, v)
	}
	if len(dupes) > 0 {
		c.warn(src.at("config.route.model.values"), ConfigFieldDropped,
			"model %q drops duplicate alias values in config.route.model.values: %s",
			m.Name, strings.Join(dupes, ", "))
	}
	return unique
}

// balancerAlgorithm returns the algorithm a model's balancer selects, defaulted as
// balancerConfig defaults it.
func balancerAlgorithm(b *aigw.Balancer) string {
	if b == nil || b.Algorithm == "" {
		return "round-robin"
	}
	return b.Algorithm
}

// warnPassthroughPolicies reports the policies reaching a passthrough model -- its own and the
// global ones -- that read the normalized LLM shape. They still load on the data plane, so this
// is a warning rather than a rejection: the configuration is valid, those policies just may not
// see what they look for.
func (c *Converter) warnPassthroughPolicies(src ConversionIssueSource, m *aigw.Model) {
	refs := slices.Clone(m.Policies)
	for _, p := range c.src.Policies {
		if p.Global != nil && *p.Global && !slices.Contains(refs, p.Name) {
			refs = append(refs, p.Name)
		}
	}
	for _, ref := range refs {
		policy := c.policies[ref]
		if policy == nil {
			continue
		}
		readsPrompt := aimap.PromptReadingPolicies[policy.Type] ||
			policy.Type == "ai-sanitizer" && aimap.SanitizerAnonymizesCredentials(policy.Config)
		if !readsPrompt {
			continue
		}
		var fields []string
		if i := slices.Index(m.Policies, ref); i >= 0 {
			fields = []string{fmt.Sprintf("policies[%d]", i)}
		}
		c.warn(src.at(fields...), BehaviorMayDiffer,
			"model %q uses the passthrough format, so policy %q (%s) may not work properly: "+
				"the body reaches the provider unchanged, in the provider's own shape",
			m.Name, ref, policy.Type)
	}
}

// isPassthrough reports whether a model forwards request bodies unchanged. The
// format is a whole-model property (convertModels rejects it alongside any other
// format), so every target of the model is passthrough or none is — which is
// also what ai-proxy-advanced requires of one plugin's targets.
func isPassthrough(m *aigw.Model) bool {
	return !formatsWildcard(m) &&
		slices.ContainsFunc(m.Formats, func(f aigw.Format) bool { return f.Type == aimap.PassthroughFormat })
}

// bodySizeOrDefault returns the ai-model-selector's max_request_body_size: at
// least aimap.DefaultMaxBodySize, raised to the model's own
// max_request_body_size (destined for ai-proxy-advanced) only if that value
// is larger, so the selector never reads less of the body than the proxy
// itself is configured to accept. The plugin reads 0 as unlimited, so a
// model value of 0 returns 0.
func bodySizeOrDefault(m *aigw.Model) int {
	if size := m.Config.MaxRequestBodySize; size != nil && (*size == 0 || *size > aimap.DefaultMaxBodySize) {
		return *size
	}
	return aimap.DefaultMaxBodySize
}

// buildModelSelectorConfig returns the ai-model-selector config a model wants
// for its route, or nil if the endpoint needs no selector at all. Explicit
// body/header/path parameters override the source. No source override leaves
// source selection to the endpoint's format/capability default.
func (c *Converter) buildModelSelectorConfig(
	src ConversionIssueSource, m *aigw.Model, spec aimap.EndpointSpec,
) []map[string]any {
	switch {
	case m.Config.Route.Model.Path.PathParam != "":
		param := m.Config.Route.Model.Path.PathParam
		// PCRE-in-path model extraction applies only when the authored paths are regex paths that actually carry
		// a named capture group for this param.
		if !pathParamCaptured(m.Config.Route.Paths, param, spec.IsRegex) {
			def := defaultModelSelectorConfig(m, spec)
			// The fallback is silent and harmless only when the format's default
			// selector still reads the model from the path (gemini/bedrock native
			// capture). For body-default formats (openai/azure) it quietly switches
			// path extraction to the request body, changing routing, per
			// KOKO-4312.
			if defaultSourceIsBody(def) {
				c.warnPathParamNotCaptured(src, m, param, spec)
			}
			return []map[string]any{def}
		}

		sources := make([]map[string]any, len(m.Config.Route.Paths))
		for i, sourcePath := range m.Config.Route.Paths {
			sources[i] = map[string]any{
				"source":                "path",
				"pcre_pattern":          sourcePath,
				"pcre_capture_name":     param,
				"max_request_body_size": bodySizeOrDefault(m),
			}
		}
		return sources

	case m.Config.Route.Model.Body.BodyParam != "":
		return []map[string]any{
			{
				"source":                "body",
				"body_path":             m.Config.Route.Model.Body.BodyParam,
				"max_request_body_size": bodySizeOrDefault(m),
			},
		}
	case m.Config.Route.Model.Header.HeaderParam != "":
		return []map[string]any{
			{
				"source":      "header",
				"header_name": m.Config.Route.Model.Header.HeaderParam,
			},
		}
	}

	return []map[string]any{defaultModelSelectorConfig(m, spec)}
}

// defaultSourceIsBody reports whether a fallback selector config reads the model
// from the request body (the ai-model-selector default when no source is set).
func defaultSourceIsBody(def map[string]any) bool {
	if def == nil {
		return false
	}
	src, _ := def["source"].(string)
	return src == "" || src == "body"
}

// warnPathParamNotCaptured reports a path_param that names a PCRE capture group
// the route paths do not provide, distinguishing a non-regex path (KOKO-4312
// point 1) from a regex path missing the named group (point 2). In both cases
// the model is read from the request body instead.
func (c *Converter) warnPathParamNotCaptured(
	src ConversionIssueSource, m *aigw.Model, param string, spec aimap.EndpointSpec,
) {
	src = src.at("config.route.model.path.path_param")
	for _, p := range m.Config.Route.Paths {
		if !spec.IsRegex && !strings.HasPrefix(p, "~") {
			c.warn(src, BehaviorMayDiffer,
				"model %q sets model.path.path_param %q but route path %q is not a regex path "+
					"(it must start with %q); the model will be read from the request body instead",
				m.Name, param, p, "~")
			return
		}
	}
	c.warn(src, BehaviorMayDiffer,
		"model %q sets model.path.path_param %q but no matching named capture group %q is present "+
			"in its regex route paths; the model will be read from the request body instead",
		m.Name, param, param)
}

func defaultModelSelectorConfig(m *aigw.Model, spec aimap.EndpointSpec) map[string]any {
	if spec.DefaultModelSelectorConfig == nil {
		return nil
	}

	// Make a copy because max_request_body_size is model-specific.
	config := make(map[string]any, len(*spec.DefaultModelSelectorConfig)+1)
	for k, v := range *spec.DefaultModelSelectorConfig {
		config[k] = v
	}
	config["max_request_body_size"] = bodySizeOrDefault(m)

	return config
}

func boolPtr(b bool) *bool { return &b }

// pathParamCaptured reports whether every authored path is a regex path (a
// decK "~" marker or a regex endpoint spec) that carries a PCRE named capture
// group for param (e.g. path_param "model_name" needs "(?<model_name>" in each
// path).
func pathParamCaptured(paths []string, param string, specIsRegex bool) bool {
	if len(paths) == 0 || param == "" {
		return false
	}
	for _, p := range paths {
		if !specIsRegex && !strings.HasPrefix(p, "~") {
			return false
		}
		if !pathHasNamedCapture(p, param) {
			return false
		}
	}
	return true
}

// pcreNamedCaptureOpen matches the opening of a PCRE named capture group in any
// of the three spellings PCRE accepts — "(?<name>", "(?P<name>", and "(?'name'"
// — capturing the group name (group 1 for the angle-bracket forms, group 2 for
// the quoted form).
var pcreNamedCaptureOpen = regexp.MustCompile(`\(\?(?:P?<([A-Za-z_][A-Za-z0-9_]*)>|'([A-Za-z_][A-Za-z0-9_]*)')`)

// namedCaptureName returns the group name from a pcreNamedCaptureOpen submatch.
func namedCaptureName(m []string) string {
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// pathHasNamedCapture reports whether s carries a PCRE named capture group named param.
func pathHasNamedCapture(s, param string) bool {
	for _, m := range pcreNamedCaptureOpen.FindAllStringSubmatch(s, -1) {
		if namedCaptureName(m) == param {
			return true
		}
	}
	return false
}

func setEndpointUpstreamURL(target map[string]any, section string, spec aimap.EndpointSpec, tm *aigw.TargetModel) {
	url, ok := aimap.EndpointUpstreamURL(section, spec, tm.Config.Options)
	if !ok {
		return
	}
	model := target["model"].(map[string]any)
	opts, _ := model["options"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
		model["options"] = opts
	}
	if opts["upstream_url"] == nil {
		opts["upstream_url"] = url
	}
}
