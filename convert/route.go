package convert

import (
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
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
	route.Protocols = transportProtocols(route.Protocols, websocket)
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

// transportProtocols changes each route protocol to its counterpart on the
// WebSocket or HTTP transport. The TLS choice of each protocol stays.
// One model route config feeds the routes of both transports.
func transportProtocols(protocols []string, websocket bool) []string {
	counterpart := map[string]string{"ws": "http", "wss": "https"}
	if websocket {
		counterpart = map[string]string{"http": "ws", "https": "wss", "ws": "ws", "wss": "wss"}
	} else if !slices.ContainsFunc(protocols, func(p string) bool { return p == "ws" || p == "wss" }) {
		return protocols
	}

	var out []string
	for _, p := range protocols {
		if mapped, ok := counterpart[p]; ok {
			p = mapped
		} else if websocket {
			continue
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if websocket && len(out) == 0 {
		return []string{"ws", "wss"}
	}
	return out
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
