package aimap

import (
	"math"
	"strings"
)

// IntelligentAlgorithm is the balancer algorithm that picks a target for
// each request from the answer of a classifier model.
const IntelligentAlgorithm = "intelligent"

// IntelligentVersion is the only version of the intelligent algorithm. The
// data plane has no field for it.
const IntelligentVersion = "v1"

// The strategy field selects the algorithm. During the tech preview only Kong
// sets it, and the public API spec leaves it out. At GA Kong sets the default
// and the field stays accepted.
const (
	IntelligentStrategyWeighted = "weighted"
	IntelligentStrategyMargin   = "margin"
)

// IntelligentStrategies are the strategies the data plane accepts. The
// first is the default.
var IntelligentStrategies = []string{IntelligentStrategyWeighted, IntelligentStrategyMargin}

// IntelligentPreset holds the data plane values of one cost_quality_tradeoff
// preset.
type IntelligentPreset struct {
	Name string
	// Tradeoff is the data plane cost_quality_tradeoff, from 0 to 1.
	Tradeoff float64
	// Margin is in points of pass rate. Only the margin strategy reads it.
	Margin float64
}

// DefaultIntelligentPreset is the preset when cost_quality_tradeoff is absent.
const DefaultIntelligentPreset = "balanced"

// The preset values. The tradeoff values are placeholders until the owner of
// the weighted algorithm sets them.
const (
	costTradeoff     = 0.25
	balancedTradeoff = 0.5
	qualityTradeoff  = 0.75
	costMargin       = 15
	balancedMargin   = 5
	qualityMargin    = 0
)

// intelligentPresets is the one table of preset values.
var intelligentPresets = []IntelligentPreset{
	{Name: "cost", Tradeoff: costTradeoff, Margin: costMargin},
	{Name: DefaultIntelligentPreset, Tradeoff: balancedTradeoff, Margin: balancedMargin},
	{Name: "quality", Tradeoff: qualityTradeoff, Margin: qualityMargin},
}

// IntelligentPresetNames returns the preset names in table order.
func IntelligentPresetNames() []string {
	names := make([]string, len(intelligentPresets))
	for i, p := range intelligentPresets {
		names[i] = p.Name
	}
	return names
}

// IntelligentPresetNamed returns the preset with the given name.
func IntelligentPresetNamed(name string) (IntelligentPreset, bool) {
	for _, p := range intelligentPresets {
		if p.Name == name {
			return p, true
		}
	}
	return IntelligentPreset{}, false
}

// NearestTradeoffPreset returns the preset whose Tradeoff is nearest v.
// exact is false when no preset has v.
func NearestTradeoffPreset(v float64) (p IntelligentPreset, exact bool) {
	return nearestPreset(v, func(p IntelligentPreset) float64 { return p.Tradeoff })
}

// NearestMarginPreset returns the preset whose Margin is nearest v. exact is
// false when no preset has v.
func NearestMarginPreset(v float64) (p IntelligentPreset, exact bool) {
	return nearestPreset(v, func(p IntelligentPreset) float64 { return p.Margin })
}

// nearestPreset returns the preset nearest v. A tie goes to the default preset.
// The default preset sits between the other two.
func nearestPreset(v float64, value func(IntelligentPreset) float64) (IntelligentPreset, bool) {
	best, _ := IntelligentPresetNamed(DefaultIntelligentPreset)
	bestDistance := math.Abs(value(best) - v)
	for _, p := range intelligentPresets {
		if d := math.Abs(value(p) - v); d < bestDistance {
			best, bestDistance = p, d
		}
	}
	return best, bestDistance == 0
}

// ClassifierContextWindow is the context window the data plane uses when a
// classifier record has none. Revert sets it on a classifier block that the
// AI Gateway API requires it on.
const ClassifierContextWindow = 32768

// ClassifierTypeCustom is the only classifier type. The classifier block
// states the URL, model name and credential of the classifier.
const ClassifierTypeCustom = "custom"

// ClassifierFormat is the API default of classifier.format. The data plane
// has no default and requires a format.
const ClassifierFormat = "/v1/systemone"

// ClassifierURLProblem tells why the data plane rejects url as a classifier
// base URL. It returns "" for a URL that the data plane accepts.
func ClassifierURLProblem(url string) string {
	rest, ok := strings.CutPrefix(url, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(url, "http://")
	}
	switch {
	case !ok:
		return "it does not use http or https"
	case strings.ContainsAny(rest, "?#"):
		return "it has a query or a fragment"
	case strings.Contains(rest, "@"):
		return "it has user information"
	case strings.HasPrefix(rest, "/") || rest == "":
		return "it has no host"
	}
	return ""
}
