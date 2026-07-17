package convert

import (
	"encoding/json"
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
	route           kong.Route
	modelExtraction aimap.ModelExtraction
	bodySize        int
	proxies         []*proxyGroup
	proxyByOwner    map[string]*proxyGroup
	wildcard        *proxyGroup
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
	seen              map[string]bool
}

// convertModels groups all (model, target, capability) tuples into routes under
// a single shared ai-gateway Service, emitting a route-scoped ai-model-selector
// and one ai-proxy-advanced per owning model (type "model") or per route (type
// "api"), plus an ai-models entry per source model. type "model" plugins are
// scoped to both the route and the ai-model entity; type "api" plugins are
// scoped route-only.
func (c *Converter) convertModels() error {
	groups := map[string]*routeGroup{}
	var order []string
	var guardPlugins []kong.Plugin
	usedRouteNames := map[string]bool{}
	identityPluginSeen := map[string]bool{}

	for i := range c.src.Models {
		m := &c.src.Models[i]
		bases := basePaths(m)
		caps := c.expandCapabilities(m)

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

		// ai-models.alias always defaults to the model name when unset (required
		// by the DP schema in both decK and db-less payloads).
		aiModelAlias := m.Config.Model.Alias
		if aiModelAlias == "" {
			aiModelAlias = m.Name
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
					if err := c.warn(
						"model %q: provider section %q has no endpoint for capability %q; skipping",
						m.Name, sec, capability); err != nil {
						return err
					}
					continue
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
				key := sec + "|" + spec.RouteLabel + "|" + identityKey + "|" + routeConfigKey
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
						modelExtraction: spec.ModelExtraction,
						bodySize:        aimap.DefaultMaxBodySize,
						proxyByOwner:    map[string]*proxyGroup{},
					}
					groups[key] = g
					order = append(order, key)
				}
				if !routeSeen[g.route.Name] {
					routeSeen[g.route.Name] = true
					routeNames = append(routeNames, g.route.Name)
				}

				// A target's own model_alias only gets the model-name default on a
				// route that extracts the model from the body:
				// that's the only case where a companion ai-model-selector plugin
				// gates entry into this model-scoped plugin on a matching
				// ai-models.alias (PR #30/KOKO-3787), so the request is guaranteed to
				// already carry body.model == alias by the time this target's own
				// "cannot use own model" check runs (see AMV-1/AG-1234). A native
				// (path-model) route has no such companion plugin, so an unaliased
				// target must stay alias-less to remain in the "<default>" balancer
				// pool — same reasoning as the type "api" fallback pool (PR #48/#49,
				// AG-1211), just gated on route shape instead of entity type.
				targetAlias := m.Config.Model.Alias
				if modelScoped && g.modelExtraction.Type == aimap.ModelExtractionBody && targetAlias == "" {
					targetAlias = m.Name
				}

				pg := g.proxyByOwner[ownerKey]
				if pg == nil {
					embeddings, err := c.resolveEmbeddings(balancerExtra(m.Config.Balancer, "embeddings"))
					if err != nil {
						return err
					}
					modelName := ""
					if modelScoped {
						modelName = m.Name
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
						seen:              map[string]bool{},
					}
					g.proxyByOwner[ownerKey] = pg
					g.proxies = append(g.proxies, pg)
				}
				target := c.buildTarget(tm, provider, providerType, targetAlias, spec.RouteType, logging)
				dedup := tm.Name + "|" + spec.RouteType
				if !pg.seen[dedup] {
					pg.seen[dedup] = true
					pg.targets = append(pg.targets, target)
				}
				if g.modelExtraction.Type != aimap.ModelExtractionNone {
					wildcard := wildcardTarget(target)
					if wildcard != nil {
						if g.wildcard == nil {
							g.wildcard = &proxyGroup{
								routeName:         g.route.Name,
								llmFormat:         pg.llmFormat,
								genaiCategory:     pg.genaiCategory,
								balancer:          pg.balancer,
								vectordb:          pg.vectordb,
								embeddings:        pg.embeddings,
								responseStreaming: pg.responseStreaming,
								modelNameHeader:   pg.modelNameHeader,
								maxBodySize:       pg.maxBodySize,
								proxy:             pg.proxy,
								seen:              map[string]bool{},
							}
						}
						wildcardDedup := dedup + "|any"
						if !g.wildcard.seen[wildcardDedup] {
							g.wildcard.seen[wildcardDedup] = true
							g.wildcard.targets = append(g.wildcard.targets, wildcard)
						}
					}
				}
				if bs := bodySizeOrDefault(m); bs > g.bodySize {
					g.bodySize = bs
				}
			}
		}

		// ai-models entry (one per source model).
		c.out.AIModels = append(c.out.AIModels, kong.AIModel{
			ID:    m.ID,
			Name:  m.Name,
			Alias: aiModelAlias,
			Tags:  c.labelsToTags(m.Labels),
		})

		// Model policy and ACL plugins scope to each route the model produces, plus
		// the ai-model entity for type "model".
		plugins, err := c.scopedPlugins(entityModel, m.Policies, m.Access.ACLs)
		if err != nil {
			return err
		}
		for _, routeName := range routeNames {
			for k := range plugins {
				p := plugins[k]
				p.Route = kong.NewStringRef(routeName)
				if modelScoped {
					p.Model = kong.NewStringRef(m.Name)
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
		if cfg := modelSelectorConfig(g.modelExtraction, g.bodySize); cfg != nil {
			c.out.Plugins = append(c.out.Plugins, kong.Plugin{
				Name:   "ai-model-selector",
				Route:  kong.NewStringRef(g.route.Name),
				Config: cfg,
			})
		}
		for _, pg := range g.proxies {
			plugin := kong.Plugin{
				Name:    "ai-proxy-advanced",
				Enabled: pg.enabled,
				Route:   kong.NewStringRef(pg.routeName),
				Config:  pg.proxyConfig(),
			}
			if pg.modelName != "" {
				plugin.Model = kong.NewStringRef(pg.modelName)
			}
			c.out.Plugins = append(c.out.Plugins, plugin)
		}
		if g.wildcard != nil && len(g.wildcard.targets) > 0 {
			c.out.Plugins = append(c.out.Plugins, kong.Plugin{
				Name:   "ai-proxy-advanced",
				Route:  kong.NewStringRef(g.wildcard.routeName),
				Config: g.wildcard.proxyConfig(),
			})
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

// modelRouteConfigKey returns a stable representation of the client-facing
// route configuration. Endpoint paths are derived separately, but the base
// paths and every other route matcher must agree before models can share a
// route.
func modelRouteConfigKey(route aigw.RouteConfig) (string, error) {
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
	}
	if tm.Name != "" {
		model["name"] = tm.Name
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
	} else if tm.Name != "" {
		target["description"] = tm.Name // Use name as default description.
	}
	if logging != nil {
		target["logging"] = logging
	}
	return target
}

// wildcardTarget duplicates a concrete target and removes model selectors from
// target.model so Kong can fall back to it when incoming requests specify an
// arbitrary model value.
func wildcardTarget(target map[string]any) map[string]any {
	model, _ := target["model"].(map[string]any)
	if len(model) == 0 {
		return nil
	}
	if _, ok := model["name"]; !ok {
		return nil
	}

	dup := make(map[string]any, len(target))
	for k, v := range target {
		dup[k] = v
	}
	modelDup := make(map[string]any, len(model))
	for k, v := range model {
		modelDup[k] = v
	}
	delete(modelDup, "name")
	delete(modelDup, "model_alias")
	dup["model"] = modelDup
	return dup
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

func llmFormat(m *aigw.Model) string {
	if len(m.Formats) > 0 && m.Formats[0].Type != "" {
		return m.Formats[0].Type
	}
	return aimap.DefaultLLMFormat
}

func bodySizeOrDefault(m *aigw.Model) int {
	if m.Config.MaxRequestBodySize != nil {
		return *m.Config.MaxRequestBodySize
	}
	return aimap.DefaultMaxBodySize
}

func boolPtr(b bool) *bool { return &b }

func modelSelectorConfig(extraction aimap.ModelExtraction, bodySize int) map[string]any {
	switch extraction.Type {
	case aimap.ModelExtractionBody:
		return map[string]any{
			"source":                "body",
			"body_path":             "model",
			"max_request_body_size": bodySize,
		}
	case aimap.ModelExtractionPath:
		if extraction.PathPattern == "" {
			return nil
		}
		return map[string]any{
			"source":       "path",
			"path_pattern": extraction.PathPattern,
		}
	default:
		return nil
	}
}
