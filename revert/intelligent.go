package revert

import (
	"maps"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// liftIntelligentBalancers changes the intelligent balancer of each reverted
// model to the AI Gateway shape.
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
	classifier, err := r.classifierBlock(model, getMap(in, "classifier"))
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

// classifierBlock changes a data plane classifier record to a balancer
// classifier block. It drops the default format.
func (r *Reverter) classifierBlock(model string, record map[string]any) (map[string]any, error) {
	if record == nil {
		return nil, r.warn("model %q: the intelligent balancer has no classifier", model)
	}
	if kind := getStr(record, "type"); kind != aimap.ClassifierTypeCustom {
		return nil, r.warn("model %q: classifier type %q is not supported. "+
			"The AI Gateway API has only type %q. config.balancer.classifier is left out.",
			model, kind, aimap.ClassifierTypeCustom)
	}
	out := map[string]any{"type": aimap.ClassifierTypeCustom}
	for _, key := range []string{"url", "format", "name", "timeout", "context_window"} {
		if v, ok := record[key]; ok {
			out[key] = v
		}
	}
	if out["format"] == aimap.ClassifierFormat {
		delete(out, "format")
	}
	if _, ok := out["context_window"]; !ok {
		// The API requires context_window. The data plane reads this value
		// when the record has none, so setting it changes nothing.
		out["context_window"] = aimap.ClassifierContextWindow
	}
	if auth := getMap(record, "auth"); len(auth) > 0 {
		header := map[string]any{}
		for key, field := range map[string]string{"name": "header_name", "value": "header_value"} {
			if v, ok := auth[field]; ok {
				header[key] = v
			}
		}
		out["auth"] = map[string]any{"type": "basic", "headers": []any{header}}
	}
	return out, nil
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
