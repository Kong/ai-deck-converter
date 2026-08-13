package convert

import (
	"encoding/json"
	"maps"
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
	// selectorOrder holds dedup keys (json of entry minus max_request_body_size),
	// in first-contributed order.
	selectorOrder []string
	// selectorByKey maps a dedup key to that entry (minus max_request_body_size).
	selectorByKey map[string]map[string]any
	selectorMax   int // largest max_request_body_size across contributors that set it
	proxies       []*proxyGroup
	proxyByOwner  map[string]*proxyGroup
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
//
// useSources selects which of the two mutually exclusive wire schemas to
// target (Options.ModelSelectorSources): false emits the legacy flat form
// (top-level source/body_path/header_name/path_pattern) — the route's key
// already guarantees at most one shape was ever contributed in that mode,
// see convertModels. true always emits config.sources, even for a single
// shape, since data planes that understand config.sources do not accept the
// legacy top-level config.source at all.
func (g *routeGroup) selectorConfig(useSources bool) map[string]any {
	if len(g.selectorOrder) == 0 {
		return nil
	}
	if !useSources {
		cfg := make(map[string]any, len(g.selectorByKey[g.selectorOrder[0]])+1)
		maps.Copy(cfg, g.selectorByKey[g.selectorOrder[0]])
		if g.selectorMax > 0 {
			cfg["max_request_body_size"] = g.selectorMax
		}
		return cfg
	}
	sources := make([]map[string]any, 0, len(g.selectorOrder))
	for _, key := range g.selectorOrder {
		sources = append(sources, g.selectorByKey[key])
	}
	cfg := map[string]any{"sources": sources}
	if g.selectorMax > 0 {
		cfg["max_request_body_size"] = g.selectorMax
	}
	return cfg
}

// proxyGroup accumulates one ai-proxy-advanced plugin: the targets owned by a
// single source model on a route (type "model", scoped to route+ai-model), or
// every target on a route (type "api", scoped route-only and merged).
type proxyGroup struct {
	routeName         string
	modelName         string // ai-model FK; empty scopes the plugin route-only
	enabled           *bool
	llmFormat         string
	genaiCategory     string
	balancer          map[string]any
	vectordb          any
	embeddings        any
	responseStreaming string
	modelNameHeader   *bool
	maxBodySize       *int
	proxy             map[string]any
	targets           []map[string]any
	targetSources     []kong.TargetSource
	source            *kong.Source
	seen              map[string]bool
}

type videoLifecycleTarget struct {
	target       *aigw.TargetModel
	provider     *aigw.Provider
	providerType string
}

// videoLifecycleCandidate owns an OpenAI-format video route. Its lifecycle
// proxy is static (not model-scoped) but retains every creation target.
type videoLifecycleCandidate struct {
	model   *aigw.Model
	targets []videoLifecycleTarget
	spec    aimap.EndpointSpec
}

// convertModels groups all (model, target, capability) tuples into routes under
// a single shared ai-gateway Service, emitting a route-scoped ai-model-selector
// and one ai-proxy-advanced per owning model (type "model") or per route (type
// "api"), plus an ai-models entry per source model. type "model" plugins are
// scoped to both the route and the ai-model entity; type "api" plugins are
// scoped route-only.
func (c *Converter) convertModels() error {
	// useSources selects which of the two mutually exclusive
	// ai-model-selector wire schemas to target; withDefaults populates the
	// pointer before c.opts is ever read, so it is never nil here.
	useSources := *c.opts.ModelSelectorSources
	groups := map[string]*routeGroup{}
	var order []string
	var guardPlugins []kong.Plugin
	usedRouteNames := map[string]bool{}
	identityPluginSeen := map[string]bool{}
	lifecycleCandidates, err := c.videoLifecycleCandidates()
	if err != nil {
		return err
	}

	for i := range c.src.Models {
		m := &c.src.Models[i]
		bases := basePaths(m)
		caps := c.expandCapabilities(m)

		// extractModelAlias yields the name a client uses to select this model: an
		// authored alias, or the model name when none is authored. It seeds both the
		// ai-models entity identity (aiModelAlias) and each type:model target's
		// model_alias (targetAlias); type:api targets stay alias-less — see the gate
		// at the buildTarget call below.
		targetAlias := extractModelAlias(m)
		aiModelAlias := targetAlias

		// ownerKey groups targets into ai-proxy-advanced plugins: per source model
		// for type "model" (each carries its own ai-model FK), shared for type
		// "api" (route-only, all targets merged into one plugin).
		modelScoped := isModelType(m)
		// API models share route-only ai-proxy-advanced plugins, so disabling one
		// plugin could disable other models on the same route. Exclude explicitly
		// disabled API models until they can be represented with independent scopes.
		if !modelScoped && m.Enabled != nil && !*m.Enabled {
			continue
		}
		ownerKey := ""
		if modelScoped {
			ownerKey = m.Name
		}

		var routeNames []string
		routeSeen := map[string]bool{}

		for j := range m.TargetModels {
			tm := &m.TargetModels[j]
			provider := c.providers[tm.Provider]
			providerType := tm.Config.Type
			if providerType == "" && provider != nil {
				providerType = provider.Type
			}
			if providerType == "" {
				if err := c.warn("model %q target %q has no resolvable provider type", m.Name, tm.Name); err != nil {
					return err
				}
			}
			if provider == nil {
				if err := c.warn(
					"model %q target %q references unknown provider %q; auth/options may be incomplete",
					m.Name, tm.Name, tm.Provider); err != nil {
					return err
				}
			}
			for _, capability := range caps {
				// The section is resolved per capability: gemini-format traffic
				// served by Vertex renders as gemini for shared capabilities
				// (generate/embeddings) but keeps the Vertex section for the
				// Vertex-only image/video/rerank endpoints.
				sec := aimap.EndpointSectionFor(llmFormat(m), providerType, capability)
				spec, ok := aimap.LookupEndpoint(sec, capability)
				if !ok {
					return c.failAt("capabilities",
						"model %q: capability %q is not supported with llm_format %q for provider type %q",
						m.Name, capability, llmFormat(m), providerType)
				}
				logging := modelLoggingBlock(withLoggingDefaults(m.Config.Logging, false, false), spec.SupportsLogStatistics)
				// Authentication plugins execute before the model selector. Models
				// with different identity-provider sets therefore cannot share a
				// route: a route-scoped auth plugin would otherwise protect every
				// model on that route.
				identityKey := identityProviderKey(m.Access.IdentityProviders)
				routeConfigKey, err := modelRouteConfigKey(m.Config.Route)
				if err != nil {
					return err
				}
				selectorCfg := buildModelSelectorConfig(m, spec)
				// The alias *value* is per-model and deliberately excluded from
				// routeConfigKey. When targeting config.sources (Options.
				// ModelSelectorSources), the selector *shape* (which source/field an
				// ai-model-selector reads) no longer forces a split: models wanting
				// different shapes still share the route, and their shapes compact
				// into config.sources (see routeGroup.addSelector). Otherwise the
				// shape stays a route-level concern like identity providers, since
				// the legacy schema's ai-model-selector can only read one shape at
				// a time: models wanting incompatible shapes cannot share a route.
				key := sec + "|" +
					spec.RouteLabel +
					"|" + identityKey +
					"|" + routeConfigKey
				if !useSources {
					key += "|" + modelSelectorShapeKey(selectorCfg)
				}
				g := groups[key]
				if g == nil {
					paths := make([]string, len(bases))
					for i, b := range bases {
						paths[i] = aimap.RoutePath(b, spec)
					}
					routeName := uniqueModelRouteName(sec+"-"+spec.RouteLabel, usedRouteNames)
					g = &routeGroup{
						route: buildModelRoute(
							m.Config.Route, routeName,
							paths, spec.Methods),
						proxyByOwner: map[string]*proxyGroup{},
					}
					g.route.Source = source("model", m.Name, "config.route")
					groups[key] = g
					order = append(order, key)
				}
				g.addSelector(selectorCfg)
				if !routeSeen[g.route.Name] {
					routeSeen[g.route.Name] = true
					routeNames = append(routeNames, g.route.Name)
				}

				pg := g.proxyByOwner[ownerKey]
				if pg == nil {
					embeddings, err := c.resolveEmbeddings(balancerExtra(m.Config.Balancer, "embeddings"))
					if err != nil {
						return err
					}
					// The plugin's model FK must equal ai_models.name — the string a
					// client sends, which ai-model-selector matches on to activate this
					// model-scoped plugin. That identity is aiModelAlias (the authored
					// alias, or the model name when none is set), not m.Name; using
					// m.Name would dangle the FK whenever an alias is authored.
					modelName := ""
					if modelScoped {
						modelName = aiModelAlias
					}

					modelNameHeader := boolPtr(false)
					if supportsModelNameHeader(spec) {
						modelNameHeader = m.Config.Model.NameHeader
					}

					pg = &proxyGroup{
						routeName:         g.route.Name,
						modelName:         modelName,
						enabled:           disabledModelPluginEnabled(m.Enabled),
						llmFormat:         llmFormat(m),
						genaiCategory:     spec.GenaiCategory,
						balancer:          balancerConfig(m.Config.Balancer),
						vectordb:          balancerExtra(m.Config.Balancer, "vectordb"),
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
						seen: map[string]bool{},
					}
					g.proxyByOwner[ownerKey] = pg
					g.proxies = append(g.proxies, pg)
				}
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
				targetModelAlias := ""
				if modelScoped {
					targetModelAlias = targetAlias
				}
				target := c.buildTarget(tm, provider, providerType, targetModelAlias, spec.RouteType, logging)
				dedup := tm.Name + "|" + spec.RouteType
				if !pg.seen[dedup] {
					pg.seen[dedup] = true
					pg.targets = append(pg.targets, target)
					pg.targetSources = append(pg.targetSources, kong.TargetSource{
						ModelName:        m.Name,
						ModelTargetIndex: j,
						Capability:       capability,
					})
				}
			}
		}

		// ai-models entry (one per source model).
		c.out.AIModels = append(c.out.AIModels, kong.AIModel{
			ID: m.ID,
			// note: name is set to alias and alias is intentionally unset to be compatible with
			// the AI Gateway 2.0.0 dataplane behavior. By setting name to alias, the AI Gateway will
			// route requests to the alias that is set in the Model's config.route.model.* properties.
			// If config.route.model.* properties are not set then aiModelAlias defaults to the model name.
			Name: aiModelAlias,
			Tags: c.labelsToTags(m.Labels),
		})

		// Model policy and ACL plugins scope to each route the model produces, plus
		// the ai-model entity for type "model".
		plugins, err := c.scopedPlugins(entityModel, m.Policies, m.Access.ACLs)
		if err != nil {
			return err
		}
		plugins = sourceScopedPlugins(plugins, "model", m.Name)
		for _, routeName := range routeNames {
			for k := range plugins {
				p := plugins[k]
				p.Route = kong.NewStringRef(routeName)
				if modelScoped {
					// Same ai-model identity as the ai-proxy-advanced FK (aiModelAlias),
					// so policy/ACL plugins scope to the entity the selector resolves.
					p.Model = kong.NewStringRef(aiModelAlias)
				}
				guardPlugins = append(guardPlugins, p)
			}
		}

		// Each route group contains only models with the same identity-provider
		// set, so these plugins can safely remain route-scoped.
		idpPlugins, err := c.scopedIdentityProviderPlugins(m.Access.IdentityProviders)
		if err != nil {
			return err
		}
		if len(idpPlugins) > 0 {
			c.ensureAnonymousConsumer()
		}
		for _, routeName := range routeNames {
			key := routeName + "\x00" + identityProviderKey(m.Access.IdentityProviders)
			if identityPluginSeen[key] {
				continue
			}
			identityPluginSeen[key] = true
			for k := range idpPlugins {
				p := idpPlugins[k]
				p.Route = kong.NewStringRef(routeName)
				guardPlugins = append(guardPlugins, p)
			}
		}
	}

	if len(order) == 0 {
		c.out.Plugins = append(c.out.Plugins, guardPlugins...)
		return nil
	}

	service := kong.Service{Name: aimap.GatewayServiceName, URL: aimap.GatewayServiceURL}
	for _, key := range order {
		g := groups[key]
		service.Routes = append(service.Routes, g.route)

		if selectorCfg := g.selectorConfig(useSources); selectorCfg != nil {
			mappings := []kong.FieldMapping{
				{GeneratedPrefix: "config.max_request_body_size", SourcePrefix: "config.max_request_body_size"},
			}
			if useSources {
				mappings = append(mappings,
					kong.FieldMapping{GeneratedPrefix: "config.sources", SourcePrefix: "config.route.model"})
			} else {
				mappings = append(mappings,
					kong.FieldMapping{GeneratedPrefix: "config.body_path", SourcePrefix: "config.route.model.body.body_param"},
					kong.FieldMapping{GeneratedPrefix: "config.header_name", SourcePrefix: "config.route.model.header.header_param"},
					kong.FieldMapping{GeneratedPrefix: "config.path_pattern", SourcePrefix: "config.route.model.path.values"},
				)
			}
			c.out.Plugins = append(c.out.Plugins, kong.Plugin{
				Name:   "ai-model-selector",
				Route:  kong.NewStringRef(g.route.Name),
				Config: selectorCfg,
				Source: source("model", g.route.Source.EntityName, "config.route.model", mappings...),
			})
		}
		for _, pg := range g.proxies {
			plugin := kong.Plugin{
				Name:          "ai-proxy-advanced",
				Enabled:       pg.enabled,
				Route:         kong.NewStringRef(pg.routeName),
				Config:        pg.proxyConfig(),
				TargetSources: pg.targetSources,
				Source:        pg.source,
			}
			if pg.modelName != "" {
				plugin.Model = kong.NewStringRef(pg.modelName)
			}
			c.out.Plugins = append(c.out.Plugins, plugin)
		}
	}
	for _, candidate := range lifecycleCandidates {
		routeName := uniqueModelRouteName("openai-videos-lifecycle", usedRouteNames)
		route := buildVideoLifecycleRoute(candidate.model.Config.Route, routeName, basePaths(candidate.model))
		route.Source = source("model", candidate.model.Name, "config.route")
		service.Routes = append(service.Routes, route)

		logging := modelLoggingBlock(
			withLoggingDefaults(candidate.model.Config.Logging, false, false),
			candidate.spec.SupportsLogStatistics,
		)
		targets := make([]map[string]any, 0, len(candidate.targets))
		targetSources := make([]kong.TargetSource, 0, len(candidate.targets))
		for targetIndex, lifecycleTarget := range candidate.targets {
			targets = append(targets, c.buildTarget(
				lifecycleTarget.target,
				lifecycleTarget.provider,
				lifecycleTarget.providerType,
				// Lifecycle requests identify a video by ID and do not carry a
				// model alias. Keep these targets in the balancer's default pool.
				"",
				candidate.spec.RouteType,
				logging,
			))
			targetSources = append(targetSources, kong.TargetSource{
				ModelName:        candidate.model.Name,
				ModelTargetIndex: targetIndex,
				Capability:       "video",
			})
		}
		pg := &proxyGroup{
			routeName:         routeName,
			enabled:           disabledModelPluginEnabled(candidate.model.Enabled),
			llmFormat:         llmFormat(candidate.model),
			genaiCategory:     candidate.spec.GenaiCategory,
			balancer:          balancerConfig(candidate.model.Config.Balancer),
			vectordb:          balancerExtra(candidate.model.Config.Balancer, "vectordb"),
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
			Enabled:       pg.enabled,
			Route:         kong.NewStringRef(routeName),
			Config:        pg.proxyConfig(),
			TargetSources: pg.targetSources,
			Source:        pg.source,
		})

		plugins, err := c.scopedPlugins(entityModel, candidate.model.Policies, candidate.model.Access.ACLs)
		if err != nil {
			return err
		}
		plugins = sourceScopedPlugins(plugins, "model", candidate.model.Name)
		for i := range plugins {
			plugins[i].Route = kong.NewStringRef(routeName)
			c.out.Plugins = append(c.out.Plugins, plugins[i])
		}
		idpPlugins, err := c.scopedIdentityProviderPlugins(candidate.model.Access.IdentityProviders)
		if err != nil {
			return err
		}
		if len(idpPlugins) > 0 {
			c.ensureAnonymousConsumer()
		}
		for i := range idpPlugins {
			idpPlugins[i].Route = kong.NewStringRef(routeName)
			c.out.Plugins = append(c.out.Plugins, idpPlugins[i])
		}
	}
	c.out.Services = append(c.out.Services, service)
	c.out.Plugins = append(c.out.Plugins, guardPlugins...)
	return nil
}

// identityProviderKey canonicalizes identity-provider references for route
// grouping. Duplicate references have no semantic effect, and their ordering
// must not make otherwise identical access policies create separate routes.
func identityProviderKey(refs []string) string {
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
func (c *Converter) videoLifecycleCandidates() ([]videoLifecycleCandidate, error) {
	var candidates []videoLifecycleCandidate
	sharedRoutes := map[string][]string{}

	for i := range c.src.Models {
		m := &c.src.Models[i]
		if !slices.Contains(c.expandCapabilities(m), "video") {
			continue
		}
		if len(m.TargetModels) == 0 {
			warning := "model %q: video lifecycle routes require at least one target; " +
				"skipping them"
			if err := c.warn(warning, m.Name); err != nil {
				return nil, err
			}
			continue
		}

		section := aimap.SectionFor(llmFormat(m), "")
		if section != "openai" {
			continue
		}
		spec, ok := aimap.LookupEndpoint(section, "video")
		if !ok {
			continue
		}
		if len(m.TargetModels) > 1 {
			warning := "model %q: video lifecycle route has multiple targets; " +
				"requests without a model alias may reach a different target than creation"
			if err := c.warn(warning, m.Name); err != nil {
				return nil, err
			}
		}
		lifecycleTargets := make([]videoLifecycleTarget, 0, len(m.TargetModels))
		for j := range m.TargetModels {
			tm := &m.TargetModels[j]
			provider := c.providers[tm.Provider]
			providerType := tm.Config.Type
			if providerType == "" && provider != nil {
				providerType = provider.Type
			}
			lifecycleTargets = append(lifecycleTargets, videoLifecycleTarget{
				target: tm, provider: provider, providerType: providerType,
			})
		}

		routeConfig := m.Config.Route
		routeConfig.Name = ""
		routeConfig.Methods = nil
		key, err := modelRouteConfigKey(routeConfig)
		if err != nil {
			return nil, err
		}
		key = section + "|" + key
		sharedRoutes[key] = append(sharedRoutes[key], m.Name)
		candidates = append(candidates, videoLifecycleCandidate{
			model: m, targets: lifecycleTargets, spec: spec,
		})
	}

	for _, models := range sharedRoutes {
		if len(models) > 1 {
			warning := "video lifecycle routes for models %s: route is shared by multiple " +
				"video models; emitting overlapping lifecycle routes"
			if err := c.warn(warning, strings.Join(models, ", ")); err != nil {
				return nil, err
			}
		}
	}
	return candidates, nil
}

func buildVideoLifecycleRoute(rc aigw.ModelRouteConfig, routeName string, bases []string) kong.Route {
	rc.Methods = nil
	paths := make([]string, 0, len(bases))
	for _, base := range bases {
		base = strings.TrimRight(base, "/")
		paths = append(paths, base+"/videos")
	}
	route := buildModelRoute(rc, routeName, paths, []string{"GET", "DELETE"})
	route.Tags = append(route.Tags, aimap.VideoLifecycleRouteTag)
	return route
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

// modelLoggingBlock maps a model's AI Gateway logging into the per-target logging
// record accepted by the ai-proxy-advanced target schema, which only allows
// log_statistics and log_payloads. Any extra keys loggingBlock may produce
// (max_payload_size, log_audits) are dropped to avoid emitting unknown fields.
func modelLoggingBlock(l *aigw.Logging, supportsLogStatistics bool) map[string]any {
	block := loggingBlock(l)
	if block == nil {
		return nil
	}

	if !supportsLogStatistics {
		block["log_statistics"] = false
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
// ai-proxy-advanced carries logging per target rather than per plugin.
func (c *Converter) buildTarget(
	tm *aigw.TargetModel, provider *aigw.Provider,
	providerType, alias, routeType string, logging map[string]any,
) map[string]any {
	model := map[string]any{
		"provider": aimap.PluginProvider(providerType),
		"name":     tm.Name,
	}
	if alias != "" {
		model["model_alias"] = alias
	}
	if opts := mapOptions(tm.Config.Options, providerType, tm.Name, provider); opts != nil {
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

// expandCapabilities normalizes a model's capabilities into canonical keys.
func (c *Converter) expandCapabilities(m *aigw.Model) []string {
	var out []string
	for _, capability := range m.Capabilities {
		out = append(out, aimap.NormalizeCapability(capability)...)
	}
	return out
}

// balancerHoisted are balancer-block fields that the plugin schema
// expects as siblings of `balancer`, not nested inside it.
var balancerHoisted = map[string]bool{"vectordb": true, "embeddings": true}

func balancerConfig(b *aigw.Balancer) map[string]any {
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

// The assistants, batches, and files endpoints do not route by model,
// as a result, they do not support the model name header.
func supportsModelNameHeader(spec aimap.EndpointSpec) bool {
	return spec.RouteType != "llm/v1/assistants" &&
		spec.RouteType != "llm/v1/batches" &&
		spec.RouteType != "llm/v1/files"
}

// isModelType reports whether a model is a synchronous "model" entity (as
// opposed to an "api" entity for files/batches). An empty type defaults to
// "model", the discriminator default and the common synchronous case.
func isModelType(m *aigw.Model) bool { return m.Type != "api" }

// disabledModelPluginEnabled returns false only when the source model is
// explicitly disabled. Omitting enabled for active models preserves Kong's
// default behavior and keeps generated configuration minimal.
func disabledModelPluginEnabled(enabled *bool) *bool {
	if enabled != nil && !*enabled {
		return enabled
	}
	return nil
}

// extractModelAlias returns the source model's authored alias, or its name when
// none is authored. Values do not override the selector source.
func extractModelAlias(m *aigw.Model) string {
	if len(m.Config.Route.Model.Values) > 0 {
		return m.Config.Route.Model.Values[0]
	}

	return m.Name
}

func llmFormat(m *aigw.Model) string {
	if len(m.Formats) > 0 && m.Formats[0].Type != "" {
		return aimap.NormalizeFormat(m.Formats[0].Type)
	}
	return aimap.DefaultLLMFormat
}

// bodySizeOrDefault returns the ai-model-selector's max_request_body_size: at
// least aimap.DefaultMaxBodySize, raised to the model's own
// max_request_body_size (destined for ai-proxy-advanced) only if that value
// is larger, so the selector never reads less of the body than the proxy
// itself is configured to accept.
func bodySizeOrDefault(m *aigw.Model) int {
	if m.Config.MaxRequestBodySize != nil && *m.Config.MaxRequestBodySize > aimap.DefaultMaxBodySize {
		return *m.Config.MaxRequestBodySize
	}
	return aimap.DefaultMaxBodySize
}

// buildModelSelectorConfig returns the ai-model-selector config a model wants
// for its route, or nil if the endpoint needs no selector at all. Explicit
// body/header/path parameters override the source. No source override leaves
// source selection to the endpoint's format/capability default.
func buildModelSelectorConfig(m *aigw.Model, spec aimap.EndpointSpec) map[string]any {
	switch {
	case m.Config.Route.Model.Path.PathParam != "":
		// ai-model-selector represents a path source with a Lua pattern, not a
		// capture-group name. The API's path_param denotes the format-provided
		// model capture, so retain that format's default path selector.
		return defaultModelSelectorConfig(m, spec)
	case m.Config.Route.Model.Body.BodyParam != "":
		return map[string]any{
			"source":                "body",
			"body_path":             m.Config.Route.Model.Body.BodyParam,
			"max_request_body_size": bodySizeOrDefault(m),
		}
	case m.Config.Route.Model.Header.HeaderParam != "":
		return map[string]any{
			"source":      "header",
			"header_name": m.Config.Route.Model.Header.HeaderParam,
		}
	}

	return defaultModelSelectorConfig(m, spec)
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

// modelSelectorShapeKey canonicalizes an ai-model-selector config's shape
// (source plus field name) for route grouping under the legacy schema,
// deliberately dropping max_request_body_size and any alias value: two
// models wanting the same shape can still share a route even if their alias
// values or body-size ceilings differ (the latter merges to the max across
// contributors), but models wanting different shapes need their own
// ai-model-selector/route, the same way models with different
// identity-provider sets do.
func modelSelectorShapeKey(cfg map[string]any) string {
	switch v, _ := cfg["source"].(string); v {
	case "body":
		bodyPath, _ := cfg["body_path"].(string)
		return "body:" + bodyPath
	case "header":
		headerName, _ := cfg["header_name"].(string)
		return "header:" + headerName
	case "path":
		return "path"
	}
	return ""
}

func boolPtr(b bool) *bool { return &b }
