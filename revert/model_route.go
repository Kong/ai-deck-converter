package revert

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// liftModelRoute changes a model route back into the route config that made
// it. It leaves out the name, the paths, and the model selector. It drops the
// values that convert.buildModelRoute sets as defaults.
func liftModelRoute(rt *kong.Route, spec *aimap.EndpointSpec) aigw.ModelRouteConfig {
	rc := aigw.ModelRouteConfig{
		Hosts:                   rt.Hosts,
		Methods:                 rt.Methods,
		Protocols:               rt.Protocols,
		Headers:                 rt.Headers,
		SNIs:                    rt.SNIs,
		Sources:                 fromKongCIDRPorts(rt.Sources),
		Destinations:            fromKongCIDRPorts(rt.Destinations),
		StripPath:               rt.StripPath,
		PreserveHost:            rt.PreserveHost,
		HTTPSRedirectStatusCode: rt.HTTPSRedirectStatusCode,
		RegexPriority:           rt.RegexPriority,
		PathHandling:            rt.PathHandling,
		RequestBuffering:        rt.RequestBuffering,
		ResponseBuffering:       rt.ResponseBuffering,
		Tags:                    rt.Tags,
	}
	if spec != nil && slices.Equal(rc.Methods, spec.Methods) {
		rc.Methods = nil
	}
	if isWebSocketRoute(rt) {
		// The WebSocket transport rewrites the protocols of the source config.
		rc.Protocols = nil
	}
	if rc.StripPath != nil && !*rc.StripPath {
		rc.StripPath = nil
	}
	return rc
}

// isWebSocketRoute reports whether rt serves the ws or wss protocol.
func isWebSocketRoute(rt *kong.Route) bool {
	return slices.ContainsFunc(rt.Protocols, func(p string) bool { return p == "ws" || p == "wss" })
}

// sameModelRoute reports whether the route configs a and b can come from one
// model. Empty methods or protocols match any value. liftModelRoute drops
// them where the endpoint or the transport sets them.
func sameModelRoute(a, b aigw.ModelRouteConfig) bool {
	if a.Methods == nil || b.Methods == nil {
		a.Methods, b.Methods = nil, nil
	}
	if a.Protocols == nil || b.Protocols == nil {
		a.Protocols, b.Protocols = nil, nil
	}
	return modelRouteKey(a) == modelRouteKey(b)
}

// methodsKey returns a key for the default methods of spec. Routes with one
// key give equal lifted methods only for equal route methods.
func methodsKey(spec *aimap.EndpointSpec) string {
	if spec == nil {
		return "\x00"
	}
	return strings.Join(spec.Methods, ",")
}

// methodsAgree reports whether rt can serve the model of g. A route with the
// same default methods as an earlier route of g must have the same methods.
func (g *modelGroup) methodsAgree(spec *aimap.EndpointSpec, rt *kong.Route) bool {
	seen, ok := g.routeMethods[methodsKey(spec)]
	return !ok || slices.Equal(seen, rt.Methods)
}

// recordMethods records the methods of rt for the default methods of spec.
func (g *modelGroup) recordMethods(spec *aimap.EndpointSpec, rt *kong.Route) {
	if g.routeMethods == nil {
		g.routeMethods = map[string][]string{}
	}
	g.routeMethods[methodsKey(spec)] = rt.Methods
}

// fillModelRoute sets the methods and protocols of dst from rc when dst has
// none.
func fillModelRoute(dst *aigw.ModelRouteConfig, rc aigw.ModelRouteConfig) {
	if dst.Methods == nil {
		dst.Methods = rc.Methods
	}
	if dst.Protocols == nil {
		dst.Protocols = rc.Protocols
	}
}

// modelRouteKey returns a stable key for the matcher and transport fields of
// rc. The paths and the model selector stay out of the key.
func modelRouteKey(rc aigw.ModelRouteConfig) string {
	rc.Name, rc.Paths, rc.Model = "", nil, aigw.ModelSelectorConfig{}
	b, err := json.Marshal(rc)
	if err != nil {
		return fmt.Sprintf("%+v", rc)
	}
	return string(b)
}

func fromKongCIDRPorts(in []kong.CIDRPort) []aigw.CIDRPort {
	if len(in) == 0 {
		return nil
	}
	out := make([]aigw.CIDRPort, 0, len(in))
	for _, item := range in {
		out = append(out, aigw.CIDRPort{IP: item.IP, Port: item.Port})
	}
	return out
}

// applyLiftedRoute copies the lifted fields of rc into dst. The paths and the
// model selector of dst stay.
func applyLiftedRoute(dst *aigw.ModelRouteConfig, rc aigw.ModelRouteConfig) {
	rc.Paths, rc.Model = dst.Paths, dst.Model
	*dst = rc
}
