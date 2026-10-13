package revert

import (
	"sort"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertServices makes a model, an MCP server, or an agent from each route.
// The AI plugin that runs on the route decides the kind.
// Each route is classified alone, so one service can mix kinds.
func (r *Reverter) revertServices() error {
	acc := &modelAcc{groups: map[string]*modelGroup{}}
	routesSeen := map[string]bool{}
	r.indexGeminiPathTargets()

	for i := range r.src.Services {
		svc := &r.src.Services[i]
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
		for j := range svc.Routes {
			rt := &svc.Routes[j]
			plugins := r.effectivePlugins(svc, rt)
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
				plainRoutes = append(plainRoutes, rt)
			}
		}

		for _, rt := range plainRoutes {
			name := svc.Name
			if len(svc.Routes) > 1 {
				name = rt.Name
			}
			if err := r.revertAgent(svc, rt, name); err != nil {
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
