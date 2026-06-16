package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// routeGroup accumulates everything that maps to a single Kong route, keyed by
// (section, routeLabel). Multiple models/targets that resolve to the same
// endpoint contribute one ai-proxy-advanced target each.
type routeGroup struct {
	route             kong.Route
	takesBodyModel    bool
	llmFormat         string
	genaiCategory     string
	balancer          map[string]any
	vectordb          any
	embeddings        any
	responseStreaming string
	modelNameHeader   *bool
	maxBodySize       *int
	bodySize          int
	targets           []map[string]any
	seen              map[string]bool
}

// convertModels groups all (model, target, capability) tuples into routes under
// a single shared ai-gateway Service, emitting ai-model-selector and
// ai-proxy-advanced plugins per route plus an ai-models entry per source model.
func (c *Converter) convertModels() error {
	groups := map[string]*routeGroup{}
	var order []string
	var guardPlugins []kong.Plugin

	for i := range c.src.Models {
		m := &c.src.Models[i]
		base := basePath(m)
		caps := c.expandCapabilities(m)

		for j := range m.TargetModels {
			tm := &m.TargetModels[j]
			provider := c.providers[tm.Provider.Name]
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
				if err := c.warn("model %q target %q references unknown provider %q; auth/options may be incomplete", m.Name, tm.Name, tm.Provider.Name); err != nil {
					return err
				}
			}
			sec := aimap.SectionFor(llmFormat(m), providerType)

			for _, capability := range caps {
				spec, ok := aimap.LookupEndpoint(sec, capability)
				if !ok {
					if err := c.warn("model %q: provider section %q has no endpoint for capability %q; skipping", m.Name, sec, capability); err != nil {
						return err
					}
					continue
				}
				key := sec + "|" + spec.RouteLabel
				g := groups[key]
				if g == nil {
					embeddings, err := c.resolveEmbeddings(balancerExtra(m.Config.Balancer, "embeddings"))
					if err != nil {
						return err
					}
					g = &routeGroup{
						route:             buildModelRoute(m.Config.Route, sec+"-"+spec.RouteLabel, aimap.RoutePath(base, spec), spec.Methods),
						takesBodyModel:    spec.TakesBodyModel,
						llmFormat:         llmFormat(m),
						genaiCategory:     spec.GenaiCategory,
						balancer:          balancerConfig(m.Config.Balancer),
						vectordb:          balancerExtra(m.Config.Balancer, "vectordb"),
						embeddings:        embeddings,
						responseStreaming: m.Config.ResponseStreaming,
						modelNameHeader:   m.Config.Model.NameHeader,
						maxBodySize:       m.Config.MaxRequestBodySize,
						bodySize:          aimap.DefaultMaxBodySize,
						seen:              map[string]bool{},
					}
					groups[key] = g
					order = append(order, key)
				}
				target := c.buildTarget(tm, provider, providerType, m.Config.Model.Alias, spec.RouteType)
				dedup := tm.Name + "|" + spec.RouteType
				if !g.seen[dedup] {
					g.seen[dedup] = true
					g.targets = append(g.targets, target)
				}
				if bs := bodySizeOrDefault(m); bs > g.bodySize {
					g.bodySize = bs
				}
			}
		}

		// ai-models entry (one per source model).
		aiModel := kong.AIModel{
			ID:    m.ID,
			Name:  m.Name,
			Alias: m.Config.Model.Alias,
		}
		if aiModel.Alias == "" {
			aiModel.Alias = m.Name
		}
		c.out.AIModels = append(c.out.AIModels, aiModel)

		// Model policy and ACL plugins scope to the ai-models entity.
		plugins, err := c.scopedPlugins(m.Policies, m.ACLs)
		if err != nil {
			return err
		}
		for k := range plugins {
			plugins[k].Model = kong.NewRef(m.Name)
			guardPlugins = append(guardPlugins, plugins[k])
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
				Route: kong.NewRef(g.route.Name),
				Config: map[string]any{
					"source":                "body",
					"body_path":             "model",
					"max_request_body_size": g.bodySize,
				},
			})
		}
		c.out.Plugins = append(c.out.Plugins, kong.Plugin{
			Name:   "ai-proxy-advanced",
			Route:  kong.NewRef(g.route.Name),
			Config: g.proxyConfig(),
		})
	}
	c.out.Services = append(c.out.Services, service)
	c.out.Plugins = append(c.out.Plugins, guardPlugins...)
	return nil
}

// proxyConfig assembles the ai-proxy-advanced plugin config for a route group.
func (g *routeGroup) proxyConfig() map[string]any {
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

// buildTarget builds one ai-proxy-advanced target from a target model.
func (c *Converter) buildTarget(tm *aigw.TargetModel, provider *aigw.Provider, providerType, alias, routeType string) map[string]any {
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

func basePath(m *aigw.Model) string {
	if len(m.Config.Route.Paths) > 0 && m.Config.Route.Paths[0] != "" {
		return m.Config.Route.Paths[0]
	}
	return aimap.DefaultBasePath
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
