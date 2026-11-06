package convert

import (
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// inferenceProxies returns the ai-proxy-advanced plugins that proxy inference,
// leaving out the generated model lists.
func inferenceProxies(doc *kong.Document) []kong.Plugin {
	var out []kong.Plugin
	for _, p := range doc.Plugins {
		if p.Name != "ai-proxy-advanced" {
			continue
		}
		if p.Config["route_type"] == aimap.ModelDirectoryRouteType {
			continue
		}
		out = append(out, p)
	}
	return out
}

// isModelListRoute reports whether route belongs to a generated model list.
func isModelListRoute(route kong.Route) bool {
	for _, path := range route.Paths {
		if strings.HasSuffix(path, aimap.ModelDirectorySpec.PathSuffix) {
			return true
		}
	}
	return false
}

// modelRoutesFor returns the routes of svc that serve a model, leaving out the
// generated model lists.
func modelRoutesFor(svc kong.Service) []kong.Route {
	var out []kong.Route
	for _, route := range svc.Routes {
		if !isModelListRoute(route) {
			out = append(out, route)
		}
	}
	return out
}

// modelRoutes returns the routes that serve a model, leaving out the generated
// model lists.
func modelRoutes(doc *kong.Document) []kong.Route {
	var out []kong.Route
	for _, svc := range doc.Services {
		out = append(out, modelRoutesFor(svc)...)
	}
	return out
}
