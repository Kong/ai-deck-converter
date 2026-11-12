package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// buildRoute converts an AI Gateway route config into a Kong Route (used by MCP
// servers and agents, which route from their own config.route).
func buildRoute(rc aigw.RouteConfig, entityName string) kong.Route {
	name := rc.Name
	if name == "" {
		name = entityName + "-route"
	}
	return kong.Route{
		Name:                    name,
		Paths:                   rc.Paths,
		Hosts:                   rc.Hosts,
		Methods:                 rc.Methods,
		Protocols:               rc.Protocols,
		Headers:                 rc.Headers,
		SNIs:                    cloneStrings(rc.SNIs),
		Sources:                 toKongCIDRPorts(rc.Sources),
		Destinations:            toKongCIDRPorts(rc.Destinations),
		StripPath:               rc.StripPath,
		PreserveHost:            rc.PreserveHost,
		HTTPSRedirectStatusCode: rc.HTTPSRedirectStatusCode,
		RegexPriority:           rc.RegexPriority,
		PathHandling:            rc.PathHandling,
		RequestBuffering:        rc.RequestBuffering,
		ResponseBuffering:       rc.ResponseBuffering,
		Tags:                    rc.Tags,
	}
}

func buildModelRoute(
	rc aigw.ModelRouteConfig, routeName string, paths []string, defaultMethods []string, websocket bool,
) kong.Route {
	route := buildRoute(aigw.RouteConfig{
		Name:                    rc.Name,
		Paths:                   rc.Paths,
		Hosts:                   rc.Hosts,
		Methods:                 rc.Methods,
		Protocols:               rc.Protocols,
		Headers:                 rc.Headers,
		SNIs:                    rc.SNIs,
		Sources:                 rc.Sources,
		Destinations:            rc.Destinations,
		StripPath:               rc.StripPath,
		PreserveHost:            rc.PreserveHost,
		HTTPSRedirectStatusCode: rc.HTTPSRedirectStatusCode,
		RegexPriority:           rc.RegexPriority,
		PathHandling:            rc.PathHandling,
		RequestBuffering:        rc.RequestBuffering,
		ResponseBuffering:       rc.ResponseBuffering,
		Tags:                    rc.Tags,
	}, routeName)
	route.Name = routeName
	route.Paths = paths
	route.Protocols = aimap.RouteProtocols(route.Protocols, websocket)
	if websocket {
		// Kong rejects methods on ws and wss routes.
		route.Methods = nil
	} else if len(route.Methods) == 0 {
		route.Methods = defaultMethods
	}
	if route.StripPath == nil {
		route.StripPath = new(false)
	}
	return route
}

func toKongCIDRPorts(in []aigw.CIDRPort) []kong.CIDRPort {
	if len(in) == 0 {
		return nil
	}
	out := make([]kong.CIDRPort, 0, len(in))
	for _, item := range in {
		out = append(out, kong.CIDRPort{
			IP:   item.IP,
			Port: item.Port,
		})
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
