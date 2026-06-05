package revert

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gperanich/ai-deck-converter/internal/aimap"
)

// endpointMatch is one resolved (capability, spec) pair for a route/target.
type endpointMatch struct {
	capability string
	spec       aimap.EndpointSpec
}

// specRegexes caches compiled Go regexes for the regex endpoint specs, keyed by
// PathSuffix. Kong uses PCRE-style named groups ("(?<name>"); Go wants "(?P<name>".
var specRegexes = struct {
	sync.Mutex
	m map[string]*regexp.Regexp
}{m: map[string]*regexp.Regexp{}}

func specRegex(suffix string) *regexp.Regexp {
	specRegexes.Lock()
	defer specRegexes.Unlock()
	if re, ok := specRegexes.m[suffix]; ok {
		return re
	}
	re, err := regexp.Compile("^" + strings.ReplaceAll(suffix, "(?<", "(?P<") + "$")
	if err != nil {
		re = nil // unmatchable; pathMatch falls back to literal-prefix only
	}
	specRegexes.m[suffix] = re
	return re
}

// resolveEndpoint recovers the (capability, spec) a route/target was generated
// from, trying progressively weaker signals:
//  1. all specs in the section whose RouteType matches the target's route_type;
//  2. narrowed by the conventional "{section}-{RouteLabel}" route name;
//  3. narrowed by the plugin's genai_category;
//  4. narrowed by matching the route path against the spec's path shape.
//
// A filter only applies when it leaves at least one candidate, so generic
// configs that break one convention still resolve via the others.
func resolveEndpoint(section, routeType, genaiCategory, routeName, routePath string) (endpointMatch, bool) {
	caps := aimap.EndpointTable[section]
	var cands []endpointMatch
	for capability, spec := range caps {
		if spec.RouteType == routeType {
			cands = append(cands, endpointMatch{capability, spec})
		}
	}
	if len(cands) == 0 {
		// Generic configs may carry a route_type the table doesn't use for
		// this section; fall back to specs positively identified by the
		// conventional route name or by the path shape.
		for capability, spec := range caps {
			byName := routeName == section+"-"+spec.RouteLabel ||
				strings.HasSuffix(routeName, "-"+spec.RouteLabel)
			_, byPath := basePathFor(routePath, spec)
			if byName || byPath {
				cands = append(cands, endpointMatch{capability, spec})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].capability < cands[j].capability })

	narrow := func(keep func(endpointMatch) bool) {
		if len(cands) <= 1 {
			return
		}
		var kept []endpointMatch
		for _, c := range cands {
			if keep(c) {
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			cands = kept
		}
	}

	narrow(func(c endpointMatch) bool {
		return routeName == section+"-"+c.spec.RouteLabel ||
			strings.HasSuffix(routeName, "-"+c.spec.RouteLabel)
	})
	if genaiCategory != "" {
		narrow(func(c endpointMatch) bool { return c.spec.GenaiCategory == genaiCategory })
	}
	narrow(func(c endpointMatch) bool {
		_, ok := basePathFor(routePath, c.spec)
		return ok
	})

	if len(cands) == 0 {
		return endpointMatch{}, false
	}
	return cands[0], true
}

// basePathFor recovers the base path a route path was built from, given the
// spec it serves (the inverse of aimap.RoutePath). Returns ok=false when the
// path does not look like it was built from the spec.
func basePathFor(path string, spec aimap.EndpointSpec) (string, bool) {
	if strings.HasPrefix(path, "~") {
		if !spec.IsRegex {
			return "", false
		}
		body := strings.TrimPrefix(path, "~")
		// Exact converter shape first: base + "/" + suffix.
		if base, ok := strings.CutSuffix(body, "/"+spec.PathSuffix); ok {
			return base, true
		}
		// Generic shape: locate the literal head of the suffix regex, then
		// verify the tail matches the full suffix pattern.
		lit := spec.PathSuffix
		if i := strings.IndexAny(lit, "([\\"); i >= 0 {
			lit = lit[:i]
		}
		idx := strings.Index(body, "/"+lit)
		if idx < 0 {
			return "", false
		}
		re := specRegex(spec.PathSuffix)
		if re == nil || !re.MatchString(body[idx+1:]) {
			return "", false
		}
		return body[:idx], true
	}
	if spec.IsRegex {
		return "", false
	}
	if base, ok := strings.CutSuffix(path, spec.PathSuffix); ok {
		return base, true
	}
	return "", false
}
