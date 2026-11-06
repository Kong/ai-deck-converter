package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// isModelDirectory reports whether the plugins of a route serve a model list.
// A model list route has no AI Gateway entity, so revert drops it.
func isModelDirectory(plugins []kong.Plugin) bool {
	for i := range plugins {
		p := plugins[i]
		if p.Name != "ai-proxy-advanced" || p.Config == nil {
			continue
		}
		if p.Config["route_type"] == aimap.ModelDirectoryRouteType {
			return true
		}
	}

	return false
}
