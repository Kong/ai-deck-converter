package aimap

import (
	"fmt"
	"maps"
	"slices"
)

const (
	// IntelligentAlgorithm is the balancer algorithm that FeatureIntelligentBalancer
	// gates.
	IntelligentAlgorithm = "intelligent"
	// DefaultCostQualityTradeoff is the API default. The data plane requires a
	// value for the intelligent algorithm.
	DefaultCostQualityTradeoff = "balanced"
	// DefaultIntelligentVersion is the only intelligent algorithm version.
	DefaultIntelligentVersion = "v1"
	// DefaultIntelligentStrategy is the strategy that the data plane uses.
	DefaultIntelligentStrategy = "weighted"
)

// IntelligentAPIOnlyKeys are intelligent balancer fields that the
// ai-proxy-advanced schema does not have.
var IntelligentAPIOnlyKeys = []string{"version", "strategy"}

// The ai-proxy-advanced cost_quality_tradeoff reads 0 as the cheapest target
// and 1 as the highest quality.
const (
	tradeoffCost     = 0.0
	tradeoffBalanced = 0.5
	tradeoffQuality  = 1.0
)

// costQualityTradeoffs maps the API values to the ai-proxy-advanced number.
var costQualityTradeoffs = map[string]float64{
	"cost":     tradeoffCost,
	"balanced": tradeoffBalanced,
	"quality":  tradeoffQuality,
}

// CostQualityTradeoffToPlugin returns the plugin number for an API value. It
// also accepts a number. It returns false for any other value.
func CostQualityTradeoffToPlugin(v any) (float64, bool) {
	if s, ok := v.(string); ok {
		n, known := costQualityTradeoffs[s]
		return n, known
	}
	n, ok := toFloat(v)
	// The negated range check also rejects NaN.
	if !ok || !(n >= tradeoffCost && n <= tradeoffQuality) {
		return 0, false
	}
	return n, true
}

// CostQualityTradeoffFromPlugin returns the API value for a plugin number. A
// number with no API value stays a number.
func CostQualityTradeoffFromPlugin(v any) any {
	n, ok := toFloat(v)
	if !ok {
		return v
	}
	for name, want := range costQualityTradeoffs {
		if n == want {
			return name
		}
	}
	return v
}

// ClassifierToPlugin changes the API classifier to the ai-proxy-advanced form.
// The API auth {type, headers: [{name, value}]} becomes {header_name,
// header_value}.
func ClassifierToPlugin(classifier map[string]any) map[string]any {
	out := maps.Clone(classifier)
	auth, ok := classifier["auth"].(map[string]any)
	if !ok {
		return out
	}
	delete(out, "auth")
	headers, _ := auth["headers"].([]any)
	if len(headers) == 0 {
		return out
	}
	header, _ := headers[0].(map[string]any)
	pluginAuth := map[string]any{}
	if name, ok := header["name"]; ok {
		pluginAuth["header_name"] = name
	}
	if value, ok := header["value"]; ok {
		pluginAuth["header_value"] = value
	}
	out["auth"] = pluginAuth
	return out
}

// ClassifierAuthDropped returns the classifier auth fields that
// ClassifierToPlugin leaves out, sorted. Each path is relative to the auth
// block.
func ClassifierAuthDropped(classifier map[string]any) []string {
	auth, ok := classifier["auth"].(map[string]any)
	if !ok {
		return nil
	}
	var dropped []string
	for _, key := range slices.Sorted(maps.Keys(auth)) {
		if key != "type" && key != "headers" {
			dropped = append(dropped, key)
		}
	}
	headers, _ := auth["headers"].([]any)
	for i := 1; i < len(headers); i++ {
		dropped = append(dropped, fmt.Sprintf("headers[%d]", i))
	}
	return dropped
}

// ClassifierFromPlugin reverses ClassifierToPlugin.
func ClassifierFromPlugin(classifier map[string]any) map[string]any {
	out := maps.Clone(classifier)
	auth, ok := classifier["auth"].(map[string]any)
	if !ok {
		return out
	}
	if _, hasName := auth["header_name"]; !hasName {
		if _, hasValue := auth["header_value"]; !hasValue {
			delete(out, "auth")
			return out
		}
	}
	header := map[string]any{}
	if name, ok := auth["header_name"]; ok {
		header["name"] = name
	}
	if value, ok := auth["header_value"]; ok {
		header["value"] = value
	}
	out["auth"] = map[string]any{"type": "basic", "headers": []any{header}}
	return out
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
