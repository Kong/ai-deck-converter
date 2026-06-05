package revert

import "github.com/gperanich/ai-deck-converter/internal/kong"

// pluginIndex normalizes plugin scoping: top-level plugins carry name-based
// foreign keys, while nested plugins inherit their parent entity. The index
// groups top-level plugins by their scope so each reversal step sees a uniform
// view (nested plugins are read in place by the step that owns the parent).
type pluginIndex struct {
	global   []kong.Plugin            // no foreign key: global plugins
	service  map[string][]kong.Plugin // service name -> plugins
	route    map[string][]kong.Plugin // route name -> plugins
	consumer map[string][]kong.Plugin // consumer username -> plugins
	group    map[string][]kong.Plugin // consumer group name -> plugins
	model    map[string][]kong.Plugin // ai-models name -> plugins
}

// buildIndexes populates the plugin index and the ai-models alias index.
func (r *Reverter) buildIndexes() {
	r.idx = pluginIndex{
		service:  map[string][]kong.Plugin{},
		route:    map[string][]kong.Plugin{},
		consumer: map[string][]kong.Plugin{},
		group:    map[string][]kong.Plugin{},
		model:    map[string][]kong.Plugin{},
	}
	for _, p := range r.src.Plugins {
		switch {
		case p.Model != nil:
			r.idx.model[p.Model.Name] = append(r.idx.model[p.Model.Name], p)
		case p.Route != nil:
			r.idx.route[p.Route.Name] = append(r.idx.route[p.Route.Name], p)
		case p.Service != nil:
			r.idx.service[p.Service.Name] = append(r.idx.service[p.Service.Name], p)
		case p.Consumer != nil:
			r.idx.consumer[p.Consumer.Name] = append(r.idx.consumer[p.Consumer.Name], p)
		case p.ConsumerGroup != nil:
			r.idx.group[p.ConsumerGroup.Name] = append(r.idx.group[p.ConsumerGroup.Name], p)
		default:
			r.idx.global = append(r.idx.global, p)
		}
	}
	for _, m := range r.src.AIModels {
		if m.Alias != "" {
			r.aiModelByAlias[m.Alias] = m.Name
		}
	}
}

// routePlugins returns the effective plugins on a route: nested plugins plus
// top-level plugins that reference the route by name.
func (r *Reverter) routePlugins(rt *kong.Route) []kong.Plugin {
	if extra := r.idx.route[rt.Name]; len(extra) > 0 {
		return append(append([]kong.Plugin{}, rt.Plugins...), extra...)
	}
	return rt.Plugins
}

// servicePlugins returns the effective plugins on a service (nested + FK).
func (r *Reverter) servicePlugins(svc *kong.Service) []kong.Plugin {
	if extra := r.idx.service[svc.Name]; len(extra) > 0 {
		return append(append([]kong.Plugin{}, svc.Plugins...), extra...)
	}
	return svc.Plugins
}

// hasPlugin reports whether the plugin list contains a plugin with the name.
func hasPlugin(plugins []kong.Plugin, name string) bool {
	return findPlugin(plugins, name) != nil
}

// findPlugin returns the first plugin with the given name, or nil.
func findPlugin(plugins []kong.Plugin, name string) *kong.Plugin {
	for i := range plugins {
		if plugins[i].Name == name {
			return &plugins[i]
		}
	}
	return nil
}
