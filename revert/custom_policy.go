package revert

import "github.com/Kong/ai-deck-converter/internal/aigw"

func (r *Reverter) revertCustomPolicies() error {
	for _, plugin := range r.src.CustomPlugins {
		if plugin.Handler == "" {
			if err := r.warn("custom plugin %q has no handler", plugin.Name); err != nil {
				return err
			}
			continue
		}
		r.out.CustomPolicies = append(r.out.CustomPolicies, aigw.CustomPolicy{
			ID:      plugin.ID,
			Name:    plugin.Name,
			Type:    "streaming",
			Schema:  plugin.Schema,
			Handler: plugin.Handler,
		})
	}
	return nil
}
