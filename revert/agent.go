package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertAgent lifts a non-AI service route back into an AI Gateway agent: an
// ai-a2a-proxy plugin marks an "a2a" agent, anything else is a plain "http"
// agent (the same shape the forward converter emits for http agents).
func (r *Reverter) revertAgent(svc *kong.Service, rt *kong.Route, name string, svcPlugins []kong.Plugin) error {
	plugins := r.routePlugins(rt)

	a := aigw.Agent{
		Type:   "http",
		Name:   name,
		Labels: r.tagsToLabels(svc.Tags),
	}
	if a2a := findPlugin(plugins, "ai-a2a-proxy"); a2a != nil {
		a.Type = "a2a"
		a.Config.Logging = loggingFromBlock(getMap(a2a.Config, "logging"))
		a.Config.MaxRequestBodySize = getInt(a2a.Config, "max_request_body_size")
	}

	a.Config.URL = serviceURL(svc)
	if a.Config.URL == "" {
		if err := r.warn("agent %q: service %q has no upstream url/host", name, svc.Name); err != nil {
			return err
		}
	}
	a.Config.Route = routeConfig(rt, name)

	refs, acls := r.policyRefs(append(append([]kong.Plugin{}, plugins...), svcPlugins...))
	a.Policies = refs
	a.Access.ACLs = acls

	r.out.Agents = append(r.out.Agents, a)
	return nil
}
