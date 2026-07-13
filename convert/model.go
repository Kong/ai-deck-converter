package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// routeGroup accumulates everything that maps to a single Kong route, keyed by
// (section, routeLabel). The route and its ai-model-selector are shared by every
// model that resolves to the endpoint; each owning model contributes its own
// ai-proxy-advanced plugin (a proxyGroup).
type routeGroup struct {
	route          kong.Route
	takesBodyModel bool
	bodySize       int
	proxies        []*proxyGroup
	proxyByOwner   map[string]*proxyGroup
}

// proxyGroup accumulates one ai-proxy-advanced plugin: the targets owned by a
// single source model on a route (type "model", scoped to route+ai-model), or
// every target on a route (type "api", scoped route-only and merged).
type proxyGroup struct {
	routeName         string
	modelName         string // ai-model FK; empty scopes the plugin route-only
	llmFormat         string
	genaiCategory     string
	balancer          map[string]any
	vectordb          any
	embeddings        any
	responseStreaming string
	modelNameHeader   *bool
	maxBodySize       *int
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

	for i := range c.src.Models {
		m := &c.src.Models[i]
		bases := basePaths(m)
		caps := c.expandCapabilities(m)
		logging := modelLoggingBlock(m.Config.Logging)

		// Preserve the source model alias on ai-proxy-advanced targets exactly as
		// authored so alias-less targets still participate in the DP's fallback
		// behavior. The ai-models entity, however, still requires an alias in both
		// decK and db-less payloads, so synthesize it from the model name when the
		// source omitted one.
		targetAlias := m.Config.Model.Alias
		aiModelAlias := targetAlias
		if aiModelAlias == "" {
			aiModelAlias = m.Name
		}

		// ownerKey groups targets into ai-proxy-advanced plugins: per source model
		// for type "model" (each carries its own ai-model FK), shared for type
		// "api" (route-only, all targets merged into one plugin).
		modelScoped := isModelType(m)
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
			sec := aimap.SectionFor(llmFormat(m), providerType)

			for _, capability := range caps {
				spec, ok := aimap.LookupEndpoint(sec, capability)
				if !ok {
					if err := c.warn(
						"model %q: provider section %q has no endpoint for capability %q; skipping",
						m.Name, sec, capability); err != nil {
						return err
					}
					continue
				}
				key := sec + "|" + spec.RouteLabel
				g := groups[key]
				if g == nil {
					paths := make([]string, len(bases))
					for i, b := range bases {
						paths[i] = aimap.RoutePath(b, spec)
					}
					g = &routeGroup{
						route: buildModelRoute(
							m.Config.Route, sec+"-"+spec.RouteLabel,
							paths, spec.Methods),
						takesBodyModel: spec.TakesBodyModel,
						bodySize:       aimap.DefaultMaxBodySize,
						proxyByOwner:   map[string]*proxyGroup{},
					}
					groups[key] = g
					order = append(order, key)
				}
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
						llmFormat:         llmFormat(m),
						genaiCategory:     spec.GenaiCategory,
						balancer:          balancerConfig(m.Config.Balancer),
						vectordb:          balancerExtra(m.Config.Balancer, "vectordb"),
						embeddings:        embeddings,
						responseStreaming: m.Config.ResponseStreaming,
						modelNameHeader:   modelNameHeader,
						maxBodySize:       m.Config.MaxRequestBodySize,
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

		// Identity provider plugins scope to the route only (no ai-model FK), and
		// require an anonymous consumer to fall back to on failed authentication.
		idpPlugins, err := c.scopedIdentityProviderPlugins(m.Access.IdentityProviders)
		if err != nil {
			return err
		}
		if len(idpPlugins) > 0 {
			c.ensureAnonymousConsumer()
		}
		for _, routeName := range routeNames {
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
		if g.takesBodyModel {
			c.out.Plugins = append(c.out.Plugins, kong.Plugin{
				Name:  "ai-model-selector",
				Route: kong.NewStringRef(g.route.Name),
				Config: map[string]any{
					"source":                "body",
					"body_path":             "model",
					"max_request_body_size": g.bodySize,
				},
			})
		}
		for _, pg := range g.proxies {
			plugin := kong.Plugin{
				Name:   "ai-proxy-advanced",
				Route:  kong.NewStringRef(pg.routeName),
				Config: pg.proxyConfig(),
			}
			if pg.modelName != "" {
				plugin.Model = kong.NewStringRef(pg.modelName)
			}
			c.out.Plugins = append(c.out.Plugins, plugin)
		}
	}
	c.out.Services = append(c.out.Services, service)
	c.out.Plugins = append(c.out.Plugins, guardPlugins...)
	return nil
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
	return cfg
}

// modelLoggingBlock maps a model's AI Gateway logging into the per-target logging
// record accepted by the ai-proxy-advanced target schema, which only allows
// log_statistics and log_payloads. Any extra keys loggingBlock may produce
// (max_payload_size, log_audits) are dropped to avoid emitting unknown fields.
func modelLoggingBlock(l *aigw.Logging) map[string]any {
	block := loggingBlock(l)
	if block == nil {
		return nil
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
	if opts := mapOptions(tm.Config.Options, providerType, provider); opts != nil {
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
