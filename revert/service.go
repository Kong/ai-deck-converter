package revert

import (
	"sort"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertServices walks every Kong service and classifies each route by the AI
// plugin it carries: ai-proxy-advanced routes accumulate into Models,
// ai-mcp-proxy routes become MCP Servers, ai-a2a-proxy and plain routes become
// Agents. Classification is per-route so hand-written configs that mix kinds
// on one service still convert.
func (r *Reverter) revertServices() error {
	acc := &modelAcc{groups: map[string]*modelGroup{}}
	routesSeen := map[string]bool{}

	for i := range r.src.Services {
		svc := &r.src.Services[i]
		svcAIPlugins, svcPlugins := splitAIPlugins(r.servicePlugins(svc))
		for j := range svc.Routes {
			routesSeen[svc.Routes[j].Name] = true
		}

		if len(svc.Routes) == 0 {
			if err := r.warn("service %q has no routes; dropped", svc.Name); err != nil {
				return err
			}
			continue
		}

		var plainRoutes []plainRoute
		hasModelRoute, hasMCPRoute := false, false
		for j := range svc.Routes {
			rt := &svc.Routes[j]
			plugins := inheritPlugins(r.routePlugins(rt), svcAIPlugins)
			switch {
			case hasPlugin(plugins, "ai-proxy-advanced"):
				if err := r.accumulateModelRoute(acc, rt, plugins); err != nil {
					return err
				}
				hasModelRoute = true
			case hasPlugin(plugins, "ai-mcp-proxy"):
				if err := r.revertMCPServer(svc, rt, plugins, svcPlugins); err != nil {
					return err
				}
				hasMCPRoute = true
			default:
				plainRoutes = append(plainRoutes, plainRoute{rt, plugins})
			}
		}

		// Service-level plugins on a pure model service have no AI Gateway home
		// (model policies scope via the ai-models FK, not the service); MCP
		// servers and agents absorb them as policies instead.
		if hasModelRoute && !hasMCPRoute && len(plainRoutes) == 0 && len(svcPlugins) > 0 {
			if err := r.warn(
				"service %q: service-level plugins on a model service have no AI Gateway representation; dropped",
				svc.Name); err != nil {
				return err
			}
		}

		for _, pr := range plainRoutes {
			name := svc.Name
			if len(svc.Routes) > 1 {
				name = pr.route.Name
			}
			if err := r.revertAgent(svc, pr.route, name, pr.plugins, svcPlugins); err != nil {
				return err
			}
		}
	}

	// Top-level plugins referencing routes that do not exist.
	var unknownRoutes []string
	for name := range r.idx.route {
		if !routesSeen[name] {
			unknownRoutes = append(unknownRoutes, name)
		}
	}
	sort.Strings(unknownRoutes)
	for _, name := range unknownRoutes {
		if err := r.warn("plugins scoped to unknown route %q; dropped", name); err != nil {
			return err
		}
	}

	return r.finalizeModels(acc)
}

// plainRoute is a route that becomes an agent, with its effective plugins.
type plainRoute struct {
	route   *kong.Route
	plugins []kong.Plugin
}

// splitAIPlugins separates service-scoped AI plugins from the other service
// plugins. Each route of the service inherits the AI plugins.
func splitAIPlugins(svcPlugins []kong.Plugin) (ai, rest []kong.Plugin) {
	for _, p := range svcPlugins {
		if aiPluginNames[p.Name] {
			ai = append(ai, p)
		} else {
			rest = append(rest, p)
		}
	}
	return ai, rest
}

// inheritPlugins adds each inherited plugin that the route does not set.
// A route-level plugin overrides a service-level plugin with the same name.
func inheritPlugins(routePlugins, inherited []kong.Plugin) []kong.Plugin {
	if len(inherited) == 0 {
		return routePlugins
	}
	out := append([]kong.Plugin{}, routePlugins...)
	for _, p := range inherited {
		if !hasPlugin(routePlugins, p.Name) {
			out = append(out, p)
		}
	}
	return out
}
