package revert

import (
	"sort"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertServices walks every Kong service and classifies each route by the AI
// plugin it carries: ai-proxy-advanced routes accumulate into Models,
// ai-mcp-proxy routes become MCP Servers, ai-a2a-proxy and plain routes become
// Agents. Classification is per-route so hand-written configs that mix kinds on
// one service still convert. Because Kong applies a service-level plugin to
// every route on the service, each route is classified and reconstructed from
// its own plugins *plus* the service-level plugins — real Konnect configs
// commonly attach the AI plugin (and its guards) at the service level.
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

		type plainRoute struct {
			rt      *kong.Route
			plugins []kong.Plugin
		}
		var plainRoutes []plainRoute
		for j := range svc.Routes {
			rt := &svc.Routes[j]
			// Effective plugins for this route: nested route plugins first,
			// then the service-level plugins that apply to every route.
			plugins := append(append([]kong.Plugin{}, r.routePlugins(rt)...), svcPlugins...)
			switch {
			case hasPlugin(plugins, "ai-proxy-advanced"):
				if err := r.accumulateModelRoute(acc, rt, plugins); err != nil {
					return err
				}
			case hasPlugin(plugins, "ai-mcp-proxy"):
				if err := r.revertMCPServer(svc, rt, plugins); err != nil {
					return err
				}
			default:
				plainRoutes = append(plainRoutes, plainRoute{rt, plugins})
			}
		}

		for _, pr := range plainRoutes {
			name := svc.Name
			if len(svc.Routes) > 1 {
				name = pr.rt.Name
			}
			if err := r.revertAgent(svc, pr.rt, name, pr.plugins); err != nil {
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
