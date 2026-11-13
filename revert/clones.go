package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// resolveCloneNames rewrites every plugin name that a cloned_plugins entry
// declares to the bundled plugin it copies. A cloned plugin is a second instance
// of that plugin, and the AI Gateway model has no clone, so this is the type
// every later step must dispatch on: policy, ACL, auth strategy or AI entity.
//
// The rewrite copies the plugin lists, so the caller's document is unchanged.
func (r *Reverter) resolveCloneNames() {
	refs := map[string]string{}
	for _, clone := range r.src.ClonedPlugins {
		if clone.Name != "" && clone.Ref != "" {
			refs[clone.Name] = clone.Ref
		}
	}
	if len(refs) == 0 {
		return
	}

	renamePlugins := func(list []kong.Plugin) []kong.Plugin {
		if len(list) == 0 {
			return list
		}
		out := make([]kong.Plugin, len(list))
		copy(out, list)
		for i := range out {
			if ref, ok := refs[out[i].Name]; ok {
				out[i].Name = ref
				continue
			}
			// A converter clone keeps its base type even when the document lost
			// its cloned_plugins entry, since its name is one the converter owns.
			if ref, ok := aimap.CloneRef(out[i].Name); ok {
				out[i].Name = ref
			}
		}
		return out
	}
	renameRoutes := func(routes []kong.Route) []kong.Route {
		if len(routes) == 0 {
			return routes
		}
		out := make([]kong.Route, len(routes))
		copy(out, routes)
		for i := range out {
			out[i].Plugins = renamePlugins(routes[i].Plugins)
		}
		return out
	}

	doc := *r.src
	doc.Plugins = renamePlugins(r.src.Plugins)
	doc.Routes = renameRoutes(r.src.Routes)

	if len(r.src.Services) > 0 {
		doc.Services = make([]kong.Service, len(r.src.Services))
		copy(doc.Services, r.src.Services)
		for i := range doc.Services {
			doc.Services[i].Plugins = renamePlugins(r.src.Services[i].Plugins)
			doc.Services[i].Routes = renameRoutes(r.src.Services[i].Routes)
		}
	}
	if len(r.src.Consumers) > 0 {
		doc.Consumers = make([]kong.Consumer, len(r.src.Consumers))
		copy(doc.Consumers, r.src.Consumers)
		for i := range doc.Consumers {
			doc.Consumers[i].Plugins = renamePlugins(r.src.Consumers[i].Plugins)
		}
	}
	if len(r.src.ConsumerGroups) > 0 {
		doc.ConsumerGroups = make([]kong.ConsumerGroup, len(r.src.ConsumerGroups))
		copy(doc.ConsumerGroups, r.src.ConsumerGroups)
		for i := range doc.ConsumerGroups {
			doc.ConsumerGroups[i].Plugins = renamePlugins(r.src.ConsumerGroups[i].Plugins)
		}
	}
	r.src = &doc
}

// warnClonedPlugins reports the clone definitions the AI Gateway model cannot
// express: the clone name, its priority and the fact that it was a separate
// instance are all dropped.
//
// A converter-emitted clone needs no warning. It exists only to run the policy
// after ai-proxy-advanced, at the converter's own priority. A clone is only
// treated as that when its ref and priority match, since a clone the converter
// did not emit would be rewritten by the next conversion.
func (r *Reverter) warnClonedPlugins() error {
	for _, clone := range r.src.ClonedPlugins {
		ref, emitted := aimap.CloneRef(clone.Name)
		if !emitted {
			if err := r.warn(
				"cloned plugin %q (ref %q) has no AI Gateway representation; its name, priority and "+
					"extra instance are dropped, and its plugins revert as %q",
				clone.Name, clone.Ref, clone.Ref); err != nil {
				return err
			}
			continue
		}
		priority := 0
		if clone.Priority != nil {
			priority = *clone.Priority
		}
		if clone.Ref != ref || priority != aimap.ResponseTransformerClonePriority {
			if err := r.warn(
				"cloned plugin %q sets ref %q and priority %d; converting again emits ref %q and "+
					"priority %d",
				clone.Name, clone.Ref, priority, ref, aimap.ResponseTransformerClonePriority); err != nil {
				return err
			}
		}
	}
	return nil
}
