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

// The strategy field is a hidden tech preview switch.
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

// ClassifierFormat is the classifier format path. The data plane joins it to
// the classifier base URL.
var ClassifierFormat = "/" + typesafeDecisionsPath()

func typesafeDecisionsPath() string {
	specs, _ := EndpointsFor(TypesafeProviderType, "decisions")
	return specs[0].PathSuffix
}

// ClassifierBaseURL returns upstreamURL without its ClassifierFormat suffix. A
// typesafe target sends to its upstream_url without a change. The classifier
// adds ClassifierFormat to a base URL.
func ClassifierBaseURL(upstreamURL string) (base string, ok bool) {
	return strings.CutSuffix(upstreamURL, ClassifierFormat)
}

// JoinClassifierURL returns the URL the data plane sends a classifier request
// to. The two parts meet at one slash.
func JoinClassifierURL(base, format string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(format, "/")
}

// ClassifierBaseURLProblem returns why the data plane rejects base as a
// classifier URL. It returns "" for a valid base.
func ClassifierBaseURLProblem(base string) string {
	rest, ok := strings.CutPrefix(base, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(base, "http://")
	}
	if !ok || rest == "" {
		return "it does not use http or https"
	}
	if strings.ContainsAny(rest, "?#") {
		return "it has a query or a fragment"
	}
	if strings.Contains(rest, "@") {
		return "it has user information"
	}
	return ""
}
