package convert

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// directoryGroup accumulates one model list route: the ai-proxy-advanced
// instances it advertises and the auth strategies that protect the route.
type directoryGroup struct {
	base       string
	route      kong.Route
	models     []string
	sources    []map[string]any
	seenIDs    map[string]bool
	strategies []string
}

func newDirectoryGroup(base string) *directoryGroup {
	return &directoryGroup{
		base: base,
		route: kong.Route{
			Paths:     []string{aimap.RoutePath(base, aimap.ModelDirectorySpec)},
			Methods:   aimap.ModelDirectorySpec.Methods,
			Protocols: []string{"http", "https"},
		},
		seenIDs: map[string]bool{},
	}
}

// add puts a model and its instances on this list. A model that shares two
// base paths contributes its instances once.
func (g *directoryGroup) add(m *aigw.Model, ids []string, access map[string]any) {
	if !containsString(g.models, m.Name) {
		g.models = append(g.models, m.Name)
	}
	for _, strategy := range m.Access.AuthStrategies {
		if !containsString(g.strategies, strategy) {
			g.strategies = append(g.strategies, strategy)
		}
	}
	for _, id := range ids {
		if g.seenIDs[id] {
			continue
		}
		g.seenIDs[id] = true
		src := map[string]any{"plugin_id": id}
		if access != nil {
			src["access"] = access
		}
		g.sources = append(g.sources, src)
	}
}

// convertModelDirectories makes one ai-proxy-advanced list route per model
// base path, plus one at the gateway root. Each list advertises the instances
// of the models it covers and carries the access rule of each one.
func (c *Converter) convertModelDirectories(st *modelPass, service *kong.Service) {
	if len(st.directorySources) == 0 {
		return
	}

	if !c.supports(aimap.FeatureModelDirectory) {
		c.reportUnsupported(issueSource(kindConversionOption, "min_runtime_version"),
			Warning, aimap.FeatureModelDirectory, "a model list route")
		return
	}

	groups, order := c.directoryGroups(st)

	for _, base := range order {
		g := groups[base]
		routeName := uniqueModelRouteName(directoryRouteName(g.base), st.usedRouteNames)
		g.route.Name = routeName
		g.route.Source = source("model", g.models[0], "config.route.paths")
		service.Routes = append(service.Routes, g.route)

		c.out.Plugins = append(c.out.Plugins, kong.Plugin{
			Name:  "ai-proxy-advanced",
			Route: kong.NewStringRef(routeName),
			Config: map[string]any{
				"route_type": aimap.ModelDirectoryRouteType,
				"models":     map[string]any{"sources": g.sources},
			},
			Source: source("model", g.models[0], "access"),
		})

		c.appendDirectoryAuth(st, routeName, g)
	}
}

// directoryGroups groups the instances of every model by base path. The root
// list covers every model; a base path list covers the models declared on it.
func (c *Converter) directoryGroups(st *modelPass) (map[string]*directoryGroup, []string) {
	groups := map[string]*directoryGroup{}
	var order []string

	add := func(base string, m *aigw.Model, ids []string, access map[string]any) {
		g := groups[base]
		if g == nil {
			g = newDirectoryGroup(base)
			groups[base] = g
			order = append(order, base)
		}
		g.add(m, ids, access)
	}

	for i := range c.src.Models {
		m := &c.src.Models[i]
		ids := st.directorySources[m.Name]
		if len(ids) == 0 {
			continue
		}

		access := directoryAccess(m.Access.ACLs)
		bases := append([]string{aimap.DefaultBasePath}, basePaths(m)...)
		for _, base := range bases {
			add(base, m, ids, access)
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i] == aimap.DefaultBasePath {
			return true
		}
		if order[j] == aimap.DefaultBasePath {
			return false
		}
		return order[i] < order[j]
	})

	return groups, order
}

// directoryAccess returns the access block of a list source. A model carries
// one rule, as the acl plugin it lowers to does.
func directoryAccess(acls aigw.ACLs) map[string]any {
	block := map[string]any{}
	rules := map[string][]string{
		"allow":      acls.Allow,
		"deny":       acls.Deny,
		"allow_when": acls.AllowWhen,
		"deny_when":  acls.DenyWhen,
	}
	for _, field := range []string{"allow", "deny", "allow_when", "deny_when"} {
		if len(rules[field]) > 0 {
			block[field] = rules[field]
		}
	}

	if len(block) == 0 {
		return nil
	}

	return block
}

// appendDirectoryAuth protects the list route with the auth strategies of the
// models it covers. A caller that matches none of them reaches the anonymous
// consumer, whose request-termination answers 401.
func (c *Converter) appendDirectoryAuth(st *modelPass, routeName string, g *directoryGroup) {
	if len(g.strategies) == 0 {
		return
	}

	src := issueSource(kindModel, g.models[0]).at("access.auth_strategies")
	plugins, err := c.scopedAuthStrategyPlugins(src, g.strategies)
	if err != nil {
		return
	}

	fallback := needsAnonymousFallback(plugins)
	if fallback {
		c.ensureAnonymousConsumer()
	}

	for i := range plugins {
		p := plugins[i]
		p.Route = kong.NewStringRef(routeName)
		if !fallback {
			p = withoutAnonymousFallback(p)
		}
		st.guardPlugins = append(st.guardPlugins, p)
	}
}

// addDirectorySource records an instance a model emits for its model lists.
func (st *modelPass) addDirectorySource(model, id string) {
	st.directorySources[model] = append(st.directorySources[model], id)
}

// directoryRouteName names the list route of a base path.
func directoryRouteName(base string) string {
	if base == aimap.DefaultBasePath {
		return "models"
	}

	name := strings.TrimPrefix(base, "~")
	name = strings.Trim(name, "/")

	var b strings.Builder
	lastDash := true
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}

	label := strings.Trim(b.String(), "-")
	if label == "" {
		return "models"
	}

	return "models-" + label
}

// modelPluginID returns the id of an ai-proxy-advanced instance, so a list
// route can reference it. The id depends on the route and the rendered plugin
// config only, which keeps a forward-revert-forward cycle byte-identical.
func modelPluginID(routeName string, cfg map[string]any) string {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return kong.StableUUID("ai-proxy-advanced:" + routeName)
	}

	return kong.StableUUID("ai-proxy-advanced:" + routeName + ":" + string(payload))
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
