package revert

import (
	"sort"

	"github.com/gperanich/ai-deck-converter/internal/kong"
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
		svcPlugins := r.servicePlugins(svc)
		for j := range svc.Routes {
			routesSeen[svc.Routes[j].Name] = true
		}

		if len(svc.Routes) == 0 {
			if err := r.warn("service %q has no routes; dropped", svc.Name); err != nil {
				return err
			}
			continue
		}

		var plainRoutes []*kong.Route
		hasModelRoute, hasMCPRoute := false, false
		for j := range svc.Routes {
			rt := &svc.Routes[j]
			plugins := r.routePlugins(rt)
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
				plainRoutes = append(plainRoutes, rt)
			}
		}

		// Service-level plugins on a pure model service have no AI Gateway home
		// (model policies scope via the ai-models FK, not the service); MCP
		// servers and agents absorb them as policies instead.
		if hasModelRoute && !hasMCPRoute && len(plainRoutes) == 0 && len(svcPlugins) > 0 {
			if err := r.warn("service %q: service-level plugins on a model service have no AI Gateway representation; dropped", svc.Name); err != nil {
				return err
			}
		}

		for _, rt := range plainRoutes {
			name := svc.Name
			if len(svc.Routes) > 1 {
				name = rt.Name
			}
			if err := r.revertAgent(svc, rt, name, svcPlugins); err != nil {
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
