package convert

import (
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// defaultRouteProtocols are the protocols of a Kong route that sets none.
var defaultRouteProtocols = []string{"http", "https"}

// routeEntry is a route of the output with the entities that own it.
type routeEntry struct {
	route  *kong.Route
	owners []ConversionIssueSource
}

// lint checks the converted output for issues
func (c *Converter) lint() {
	c.warnRoutingConflicts()
}

// warnRoutingConflicts warns for each pair of output routes that can match
// one request. The issue goes to each owner of the later route.
func (c *Converter) warnRoutingConflicts() {
	entries := c.routeEntries()
	for i := range entries {
		later := entries[i]
		for _, earlier := range entries[:i] {
			if !routesOverlap(*earlier.route, *later.route) || sharedLifecycleRoutes(*earlier.route, *later.route) ||
				routesRefine(*earlier.route, *later.route) {
				continue
			}
			for _, owner := range later.owners {
				if slices.ContainsFunc(earlier.owners, owner.sameEntity) {
					continue
				}
				c.warn(owner, RoutingConflict,
					"route %q of %s %q can match the same requests as route %q of %s. "+
						"Kong sends each such request to only one of these routes.",
					later.route.Name, owner.Kind, owner.Name, earlier.route.Name, ownerNames(earlier.owners))
			}
		}
	}
}

// routeEntries returns the routes of every enabled service and the top-level
// routes, in document order. A route without a source has no owner to
// report, so it is left out.
func (c *Converter) routeEntries() []routeEntry {
	var entries []routeEntry
	add := func(route *kong.Route) {
		if owners := c.routeOwners(route); len(owners) > 0 {
			entries = append(entries, routeEntry{route: route, owners: owners})
		}
	}
	for i := range c.out.Services {
		service := &c.out.Services[i]
		if service.Enabled != nil && !*service.Enabled {
			continue
		}
		for j := range service.Routes {
			add(&service.Routes[j])
		}
	}
	for i := range c.out.Routes {
		add(&c.out.Routes[i])
	}
	return entries
}

// routeOwners returns the issue sources of the entities that own route. Every
// model on a shared model route owns it.
func (c *Converter) routeOwners(route *kong.Route) []ConversionIssueSource {
	if route.Source == nil {
		return nil
	}
	field := route.Source.FieldPrefix
	if route.Source.EntityType == kindModel {
		if models, ok := c.modelRouteModels[route.Name]; ok && len(models) > 0 {
			owners := make([]ConversionIssueSource, len(models))
			for i, model := range models {
				owners[i] = issueSource(kindModel, model).at(field)
			}
			return owners
		}
	}
	return []ConversionIssueSource{issueSource(route.Source.EntityType, route.Source.EntityName).at(field)}
}

func (s ConversionIssueSource) sameEntity(other ConversionIssueSource) bool {
	return s.Kind == other.Kind && s.Name == other.Name
}

func ownerNames(owners []ConversionIssueSource) string {
	names := make([]string, len(owners))
	for i, owner := range owners {
		names[i] = fmt.Sprintf("%s %q", owner.Kind, owner.Name)
	}
	return strings.Join(names, ", ")
}

// sharedLifecycleRoutes reports whether a and b are video lifecycle routes
// with equal matchers. videoLifecycleCandidates already reports them. It
// gives no issue for a model whose video comes from "*".
func sharedLifecycleRoutes(a, b kong.Route) bool {
	isLifecycle := func(r kong.Route) bool { return slices.Contains(r.Tags, aimap.VideoLifecycleRouteTag) }
	if !isLifecycle(a) || !isLifecycle(b) {
		return false
	}
	a.Name, b.Name = "", ""
	a.Plugins, b.Plugins = nil, nil
	a.Source, b.Source = nil, nil
	return reflect.DeepEqual(a, b)
}

// routesRefine reports whether one of a and b refines the other. Kong sends
// a request that matches both routes to the refining route.
func routesRefine(a, b kong.Route) bool {
	return refines(a, b) || refines(b, a)
}

// refines reports whether each request that matches specific also matches
// general, and specific adds hosts or header names. Kong gives a route with
// hosts or with more headers a higher priority.
func refines(specific, general kong.Route) bool {
	addsMatcher := len(general.Hosts) == 0 && len(specific.Hosts) > 0 ||
		len(specific.Headers) > len(general.Headers)
	return addsMatcher &&
		listWithin(protocolsOrDefault(specific.Protocols), protocolsOrDefault(general.Protocols), strings.EqualFold) &&
		listWithin(specific.Methods, general.Methods, strings.EqualFold) &&
		listWithin(specific.Paths, general.Paths, func(x, y string) bool { return x == y }) &&
		listWithin(specific.Hosts, general.Hosts, strings.EqualFold) &&
		listWithin(specific.SNIs, general.SNIs, strings.EqualFold) &&
		listWithin(specific.Sources, general.Sources, sameCIDRPort) &&
		listWithin(specific.Destinations, general.Destinations, sameCIDRPort) &&
		headersWithin(specific.Headers, general.Headers)
}

// listWithin reports whether each value of a is in b. An empty b matches
// every value. An empty a matches every value, so it is not within a
// non-empty b.
func listWithin[T any](a, b []T, equal func(x, y T) bool) bool {
	if len(b) == 0 {
		return true
	}
	if len(a) == 0 {
		return false
	}
	for _, x := range a {
		if !slices.ContainsFunc(b, func(y T) bool { return equal(x, y) }) {
			return false
		}
	}
	return true
}

// headersWithin reports whether a sets each header of b with values that b
// accepts.
func headersWithin(a, b map[string][]string) bool {
	for nameB, valuesB := range b {
		found := false
		for nameA, valuesA := range a {
			if strings.EqualFold(nameA, nameB) {
				found = listWithin(valuesA, valuesB, headerValuesOverlap)
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameCIDRPort(a, b kong.CIDRPort) bool {
	return a.IP == b.IP && reflect.DeepEqual(a.Port, b.Port)
}

// routesOverlap reports whether one request can match both a and b.
func routesOverlap(a, b kong.Route) bool {
	return listsOverlap(protocolsOrDefault(a.Protocols), protocolsOrDefault(b.Protocols), strings.EqualFold) &&
		listsOverlap(a.Methods, b.Methods, strings.EqualFold) &&
		listsOverlap(a.Paths, b.Paths, func(x, y string) bool { return x == y }) &&
		listsOverlap(a.Hosts, b.Hosts, hostsOverlap) &&
		listsOverlap(a.SNIs, b.SNIs, hostsOverlap) &&
		headersOverlap(a.Headers, b.Headers) &&
		listsOverlap(a.Sources, b.Sources, cidrPortsOverlap) &&
		listsOverlap(a.Destinations, b.Destinations, cidrPortsOverlap)
}

func protocolsOrDefault(protocols []string) []string {
	if len(protocols) == 0 {
		return defaultRouteProtocols
	}
	return protocols
}

// listsOverlap reports whether a and b overlap. An empty list matches every
// value.
func listsOverlap[T any](a, b []T, overlap func(x, y T) bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if overlap(x, y) {
				return true
			}
		}
	}
	return false
}

// hostsOverlap reports whether one host name matches both host patterns.
// A pattern has at most one wildcard, at its start or at its end.
func hostsOverlap(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	aSuffix, aLeading := strings.CutPrefix(a, "*")
	aPrefix, aTrailing := strings.CutSuffix(a, "*")
	bSuffix, bLeading := strings.CutPrefix(b, "*")
	bPrefix, bTrailing := strings.CutSuffix(b, "*")
	switch {
	case aLeading && bLeading:
		return strings.HasSuffix(aSuffix, bSuffix) || strings.HasSuffix(bSuffix, aSuffix)
	case aTrailing && bTrailing:
		return strings.HasPrefix(aPrefix, bPrefix) || strings.HasPrefix(bPrefix, aPrefix)
	case aLeading && bTrailing, aTrailing && bLeading:
		return true
	case aLeading:
		return strings.HasSuffix(b, aSuffix)
	case aTrailing:
		return strings.HasPrefix(b, aPrefix)
	case bLeading:
		return strings.HasSuffix(a, bSuffix)
	case bTrailing:
		return strings.HasPrefix(a, bPrefix)
	}
	return a == b
}

// headersOverlap reports whether one request can match the headers of a
// and b. A header that only one route sets does not limit the other route.
func headersOverlap(a, b map[string][]string) bool {
	for nameA, valuesA := range a {
		for nameB, valuesB := range b {
			if strings.EqualFold(nameA, nameB) && !listsOverlap(valuesA, valuesB, headerValuesOverlap) {
				return false
			}
		}
	}
	return true
}

// headerValuesOverlap compares two header values. A regex value overlaps
// only an equal regex value.
func headerValuesOverlap(a, b string) bool {
	isRegex := func(v string) bool { return strings.HasPrefix(v, "~") }
	if isRegex(a) || isRegex(b) {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// cidrPortsOverlap reports whether one address matches both a and b. An
// unset IP or port matches every value.
func cidrPortsOverlap(a, b kong.CIDRPort) bool {
	if a.Port != nil && b.Port != nil && *a.Port != *b.Port {
		return false
	}
	if a.IP == "" || b.IP == "" {
		return true
	}
	prefixA, errA := parsePrefix(a.IP)
	prefixB, errB := parsePrefix(b.IP)
	if errA != nil || errB != nil {
		return a.IP == b.IP
	}
	return prefixA.Overlaps(prefixB)
}

// parsePrefix accepts a CIDR range or a single IP address.
func parsePrefix(s string) (netip.Prefix, error) {
	if !strings.Contains(s, "/") {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.ParsePrefix(s)
}
