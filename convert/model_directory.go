package convert

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// directoryGroup accumulates one model list route: the ai-proxy-advanced
// instances it advertises and the auth strategies that protect the route.
type directoryGroup struct {
	nameBase   string
	path       string
	route      kong.Route
	models     []string
	sources    []map[string]any
	seenIDs    map[string]bool
	strategies []string
}

func newDirectoryGroup(nameBase, path string) *directoryGroup {
	return &directoryGroup{
		nameBase: nameBase,
		path:     path,
		route: kong.Route{
			Paths:     []string{path},
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
	if !c.supports(aimap.FeatureModelDirectory) {
		if len(service.Routes) > 0 {
			c.reportUnsupported(issueSource(kindConversionOption, "min_runtime_version"),
				Warning, aimap.FeatureModelDirectory, "a model list route")
		}
		return
	}

	if len(st.directorySources) == 0 {
		return
	}

	groups, order := c.directoryGroups(st)

	for _, key := range order {
		g := groups[key]
		if len(g.sources) == 0 {
			continue
		}
		routeName := uniqueModelRouteName(directoryRouteName(g.nameBase), st.usedRouteNames)
		g.route.Name = routeName
		g.route.Source = source("model", g.models[0], "config.route.paths")
		service.Routes = append(service.Routes, g.route)

		sort.SliceStable(g.sources, func(i, j int) bool {
			return g.sources[i]["plugin_id"].(string) < g.sources[j]["plugin_id"].(string)
		})

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

// directoryGroups groups the instances of every model by list path. The root
// list covers every model; a base path list covers the models declared on it.
func (c *Converter) directoryGroups(st *modelPass) (map[string]*directoryGroup, []string) {
	groups := map[string]*directoryGroup{}
	var order []string

	access := map[string]map[string]any{}
	modelsByID := map[string][]string{}
	for i := range c.src.Models {
		m := &c.src.Models[i]
		for _, id := range st.directorySources[m.Name] {
			modelsByID[id] = append(modelsByID[id], m.Name)
			access[m.Name] = directoryAccess(m.Access.ACLs)
		}
	}

	conflicted := c.conflictingDirectoryIDs(modelsByID, access)

	add := func(base string, m *aigw.Model, ids []string) {
		path := aimap.RoutePath(base, aimap.ModelDirectorySpec)
		g := groups[path]
		if g == nil {
			g = newDirectoryGroup(base, path)
			groups[path] = g
			order = append(order, path)
		}
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if !conflicted[id] {
				kept = append(kept, id)
			}
		}
		g.add(m, kept, access[m.Name])
	}

	for i := range c.src.Models {
		m := &c.src.Models[i]
		ids := st.directorySources[m.Name]
		if len(ids) == 0 {
			continue
		}

		bases := append([]string{aimap.DefaultBasePath}, basePaths(m)...)
		for _, base := range bases {
			add(base, m, ids)
		}
	}

	root := aimap.RoutePath(aimap.DefaultBasePath, aimap.ModelDirectorySpec)
	sort.Slice(order, func(i, j int) bool {
		if order[i] == root {
			return true
		}
		if order[j] == root {
			return false
		}
		return order[i] < order[j]
	})

	return groups, order
}

// conflictingDirectoryIDs reports the instances that two models share with
// different access rules. A list carries one rule per source, so such an
// instance cannot be advertised without hiding or showing a model wrongly.
func (c *Converter) conflictingDirectoryIDs(
	modelsByID map[string][]string, access map[string]map[string]any,
) map[string]bool {
	conflicted := map[string]bool{}

	var ids []string
	for id := range modelsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		models := modelsByID[id]
		for _, other := range models[1:] {
			if reflect.DeepEqual(access[models[0]], access[other]) {
				continue
			}
			conflicted[id] = true
			c.warn(issueSource(kindModel, other).at("access.acls"), BehaviorMayDiffer,
				"models %q and %q share one ai-proxy-advanced instance but set different access rules. "+
					"The model list leaves that instance out, so neither model is advertised from it.",
				models[0], other)
			break
		}
	}

	return conflicted
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
// config only, which keeps a forward-revert-forward cycle byte-identical. A
// second instance with the same route and config takes the next occurrence.
func modelPluginID(routeName string, cfg map[string]any, occurrence int) string {
	key := "ai-proxy-advanced:" + routeName
	if payload, err := json.Marshal(cfg); err == nil {
		key += ":" + string(payload)
	}
	if occurrence > 1 {
		key += ":" + strconv.Itoa(occurrence)
	}

	return kong.StableUUID(key)
}

// nextModelPluginID returns an id that no other instance of this pass has.
func (c *Converter) nextModelPluginID(st *modelPass, routeName string, cfg map[string]any) string {
	for occurrence := 1; ; occurrence++ {
		id := modelPluginID(routeName, cfg, occurrence)
		if !st.usedPluginIDs[id] {
			st.usedPluginIDs[id] = true
			return id
		}
	}
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
