package revert

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// liftIntelligentBalancers changes the intelligent balancer of each reverted
// model to the AI Gateway shape. A classifier names another model. This runs
// after every model and provider exists.
func (r *Reverter) liftIntelligentBalancers() error {
	for i := range r.out.Models {
		m := &r.out.Models[i]
		if m.Config.Balancer == nil || m.Config.Balancer.Algorithm != aimap.IntelligentAlgorithm {
			continue
		}
		fields, err := r.liftIntelligentFields(m.Name, m.Config.Balancer.Fields)
		if err != nil {
			return err
		}
		m.Config.Balancer = &aigw.Balancer{Algorithm: aimap.IntelligentAlgorithm, Fields: fields}
	}
	return nil
}

// liftIntelligentFields reverses convert.lowerIntelligentBalancer. It drops
// the default preset and the default strategy.
func (r *Reverter) liftIntelligentFields(model string, in map[string]any) (map[string]any, error) {
	fields := maps.Clone(in)
	strategy := getStr(fields, "strategy")
	if strategy != "" && !slices.Contains(aimap.IntelligentStrategies, strategy) {
		if err := r.warn("model %q: intelligent balancer strategy %q is unknown", model, strategy); err != nil {
			return nil, err
		}
	}
	preset, err := r.intelligentPreset(model, fields, strategy)
	if err != nil {
		return nil, err
	}
	delete(fields, "cost_quality_tradeoff")
	delete(fields, "margin")
	delete(fields, "strategy")
	delete(fields, "classifier")
	if preset.Name != aimap.DefaultIntelligentPreset {
		fields["cost_quality_tradeoff"] = preset.Name
	}
	if strategy != "" && strategy != aimap.IntelligentStrategies[0] {
		fields["strategy"] = strategy
	}
	classifier, err := r.classifierRef(model, getMap(in, "classifier"))
	if err != nil {
		return nil, err
	}
	if len(classifier) > 0 {
		fields["classifier"] = classifier
	}
	return fields, nil
}

// intelligentPreset returns the preset nearest the numbers of a data plane
// balancer block. The margin strategy reads the margin. Any other strategy
// reads the tradeoff.
func (r *Reverter) intelligentPreset(
	model string, fields map[string]any, strategy string,
) (aimap.IntelligentPreset, error) {
	tradeoff, hasTradeoff := number(fields["cost_quality_tradeoff"])
	margin, hasMargin := number(fields["margin"])
	if strategy == aimap.IntelligentStrategyMargin && hasMargin {
		p, exact := aimap.NearestMarginPreset(margin)
		if !exact {
			if err := r.warn("model %q: margin %v is not a preset value. "+
				"It becomes the nearest preset %q, with margin %v.", model, margin, p.Name, p.Margin); err != nil {
				return p, err
			}
		}
		if hasTradeoff && tradeoff != p.Tradeoff {
			if err := r.warn("model %q: cost_quality_tradeoff %v becomes %v, the value of preset %q.",
				model, tradeoff, p.Tradeoff, p.Name); err != nil {
				return p, err
			}
		}
		return p, nil
	}
	if hasMargin {
		if err := r.warn("model %q: margin %v is dropped. Only the margin strategy reads it.",
			model, margin); err != nil {
			return aimap.IntelligentPreset{}, err
		}
	}
	p, _ := aimap.IntelligentPresetNamed(aimap.DefaultIntelligentPreset)
	if !hasTradeoff {
		return p, nil
	}
	p, exact := aimap.NearestTradeoffPreset(tradeoff)
	if !exact {
		if err := r.warn("model %q: cost_quality_tradeoff %v is not a preset value. "+
			"It becomes the nearest preset %q, with value %v.", model, tradeoff, p.Name, p.Tradeoff); err != nil {
			return p, err
		}
	}
	return p, nil
}

// classifierRef changes a data plane classifier record to a balancer
// classifier block. The block names the reverted model that serves the
// classifier.
func (r *Reverter) classifierRef(model string, block map[string]any) (map[string]any, error) {
	if block == nil {
		return nil, r.warn("model %q: the intelligent balancer has no classifier", model)
	}
	out := map[string]any{}
	for _, key := range []string{"timeout", "context_window"} {
		if v, ok := block[key]; ok {
			out[key] = v
		}
	}
	name, problem := r.classifierModel(block)
	if name == "" {
		return out, r.warn("model %q: %s. classifier.model is left out. "+
			"Set it to a decisions model with one typesafe target.", model, problem)
	}
	out["model"] = name
	return out, nil
}

// classifierModel returns the first reverted model that serves the
// classifier record. It returns why no model matches when name is "".
func (r *Reverter) classifierModel(block map[string]any) (name, problem string) {
	target := getStr(block, "name")
	upstream := ""
	switch kind := getStr(block, "type"); kind {
	case "model":
		if p := getStr(block, "provider"); p != aimap.TypesafeProviderType {
			return "", fmt.Sprintf("the classifier provider %q is not %s", p, aimap.TypesafeProviderType)
		}
	case "custom":
		upstream = aimap.JoinClassifierURL(getStr(block, "url"), getStr(block, "format"))
	default:
		return "", fmt.Sprintf("the classifier type %q is not model or custom", kind)
	}
	auth := getMap(block, "auth")
	header := aigw.AuthHeader{Name: getStr(auth, "header_name"), Value: getStr(auth, "header_value")}
	for _, m := range r.out.Models {
		if r.servesClassifier(m, target, upstream, header) {
			return m.Name, ""
		}
	}
	return "", fmt.Sprintf("no decisions model has the classifier target %q", target)
}

// servesClassifier reports whether m has one typesafe target with the given
// name, upstream_url and provider auth header.
func (r *Reverter) servesClassifier(m aigw.Model, target, upstream string, header aigw.AuthHeader) bool {
	if !slices.Contains(m.Capabilities, "decisions") || len(m.TargetModels) != 1 ||
		!slices.Contains(m.Formats, aigw.Format{Type: aimap.TypesafeProviderType}) {
		return false
	}
	tm := m.TargetModels[0]
	i := slices.IndexFunc(r.providers, func(p aigw.Provider) bool { return p.Name == tm.Provider })
	if i < 0 || tm.Name != target {
		return false
	}
	provider := r.providers[i]
	if cmp.Or(tm.Config.Type, provider.Type) != aimap.TypesafeProviderType {
		return false
	}
	if got, _ := tm.Config.Options["upstream_url"].(string); classifierURL(got) != upstream {
		return false
	}
	auth := provider.Config.Auth
	var got aigw.AuthHeader
	if len(auth.Headers) > 0 {
		got = auth.Headers[0]
	}
	return len(auth.Params) == 0 && got == header
}

// classifierURL returns the URL that a classifier made from a target
// upstream_url calls. It returns "" for a target with no upstream_url.
func classifierURL(upstream string) string {
	if upstream == "" {
		return ""
	}
	base, format, problem := aimap.SplitClassifierURL(upstream)
	if problem != "" {
		return upstream
	}
	return aimap.JoinClassifierURL(base, format)
}

// number returns a YAML number as a float64.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
