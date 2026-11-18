package convert

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

const classifierField = "config.balancer.classifier"

// classifierKeys are the fields a balancer.classifier block can hold.
var classifierKeys = []string{"context_window", "model", "timeout"}

// modelBalancer returns the ai-proxy-advanced balancer block of m. It fails m
// for an intelligent balancer that the data plane cannot run.
func (c *Converter) modelBalancer(src ConversionIssueSource, m *aigw.Model) (map[string]any, error) {
	cfg := c.balancerConfig(m.Config.Balancer)
	if cfg["algorithm"] != aimap.IntelligentAlgorithm {
		return cfg, nil
	}
	if err := c.lowerIntelligentBalancer(src, m, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// lowerIntelligentBalancer changes the preset, strategy and classifier fields
// of cfg to the data plane shape. cfg is a copy of the source block.
func (c *Converter) lowerIntelligentBalancer(src ConversionIssueSource, m *aigw.Model, cfg map[string]any) error {
	if !c.supports(aimap.FeatureIntelligentBalancer) {
		return c.failUnsupported(src.at("config.balancer.algorithm"), aimap.FeatureIntelligentBalancer,
			"config.balancer.algorithm: "+aimap.IntelligentAlgorithm)
	}
	if v, ok := cfg["version"]; ok && v != aimap.IntelligentVersion {
		return c.fail(src.at("config.balancer.version"), ConfigFieldInvalid,
			"model %q: intelligent balancer version %#v is not supported. Use %q.",
			m.Name, v, aimap.IntelligentVersion)
	}
	delete(cfg, "version")

	presetName := aimap.DefaultIntelligentPreset
	if v, ok := cfg["cost_quality_tradeoff"]; ok {
		presetName, _ = v.(string)
	}
	preset, ok := aimap.IntelligentPresetNamed(presetName)
	if !ok {
		return c.fail(src.at("config.balancer.cost_quality_tradeoff"), ConfigFieldInvalid,
			"model %q: cost_quality_tradeoff %#v is not one of %s",
			m.Name, cfg["cost_quality_tradeoff"], strings.Join(aimap.IntelligentPresetNames(), ", "))
	}
	strategy := aimap.IntelligentStrategies[0]
	if v, ok := cfg["strategy"]; ok {
		strategy, _ = v.(string)
	}
	if !slices.Contains(aimap.IntelligentStrategies, strategy) {
		return c.fail(src.at("config.balancer.strategy"), ConfigFieldInvalid,
			"model %q: intelligent balancer strategy %#v is not one of %s",
			m.Name, cfg["strategy"], strings.Join(aimap.IntelligentStrategies, ", "))
	}
	if _, ok := cfg["margin"]; ok {
		return c.fail(src.at("config.balancer.margin"), ConfigFieldInvalid,
			"model %q: config.balancer.margin is not a model field. The cost_quality_tradeoff preset sets it.",
			m.Name)
	}
	cfg["cost_quality_tradeoff"] = preset.Tradeoff
	cfg["strategy"] = strategy
	if strategy == aimap.IntelligentStrategyMargin {
		cfg["margin"] = preset.Margin
	}

	classifier, err := c.classifierConfig(src, m, cfg["classifier"])
	if err != nil {
		return err
	}
	cfg["classifier"] = classifier
	return nil
}

// classifierConfig changes a balancer.classifier block to the data plane
// classifier record. The block names a decisions model of the document. The
// record copies that model's one target and its provider auth header.
func (c *Converter) classifierConfig(src ConversionIssueSource, m *aigw.Model, raw any) (map[string]any, error) {
	if raw == nil {
		return nil, c.fail(src.at(classifierField), MissingRequiredConfigField,
			"model %q: the intelligent balancer needs %s.model, the name of a decisions model",
			m.Name, classifierField)
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, c.fail(src.at(classifierField), ConfigFieldInvalid,
			"model %q: %s is not a map", m.Name, classifierField)
	}
	for _, key := range slices.Sorted(maps.Keys(block)) {
		if !slices.Contains(classifierKeys, key) {
			return nil, c.fail(src.at(classifierField+"."+key), ConfigFieldInvalid,
				"model %q: %s.%s is not a classifier field. The fields are %s.",
				m.Name, classifierField, key, strings.Join(classifierKeys, ", "))
		}
	}
	modelSrc := src.at(classifierField + ".model")
	name, _ := block["model"].(string)
	if name == "" {
		return nil, c.fail(modelSrc, MissingRequiredConfigField,
			"model %q: the intelligent balancer needs %s.model, the name of a decisions model",
			m.Name, classifierField)
	}
	classifier := c.modelNamed(name)
	switch {
	case classifier == nil:
		return nil, c.fail(modelSrc, UndefinedReference,
			"model %q: classifier model %q does not exist", m.Name, name)
	case isDisabled(classifier):
		return nil, c.fail(modelSrc, ConfigFieldInvalid,
			"model %q: classifier model %q is disabled", m.Name, name)
	case !servesDecisions(classifier):
		return nil, c.fail(modelSrc, ConfigFieldInvalid,
			"model %q: classifier model %q does not serve the decisions capability in the %s format",
			m.Name, name, aimap.TypesafeProviderType)
	case len(classifier.TargetModels) != 1:
		return nil, c.fail(modelSrc, ConfigFieldInvalid,
			"model %q: classifier model %q has %d targets. A classifier model needs exactly one target.",
			m.Name, name, len(classifier.TargetModels))
	}

	tm := &classifier.TargetModels[0]
	provider := c.providers[tm.Provider]
	if provider == nil {
		return nil, c.fail(modelSrc, UndefinedReference,
			"model %q: target %q of classifier model %q references unknown provider %q",
			m.Name, tm.Name, name, tm.Provider)
	}
	if providerType := cmp.Or(tm.Config.Type, provider.Type); providerType != aimap.TypesafeProviderType {
		return nil, c.fail(modelSrc, ConfigFieldInvalid,
			"model %q: target %q of classifier model %q has provider type %q. "+
				"A classifier must speak System One, which needs provider type %q.",
			m.Name, tm.Name, name, providerType, aimap.TypesafeProviderType)
	}
	auth, err := c.classifierAuth(modelSrc, m, name, provider)
	if err != nil {
		return nil, err
	}

	out := map[string]any{"name": tm.Name}
	if raw, ok := tm.Config.Options["upstream_url"]; ok {
		upstream, _ := raw.(string)
		base, ok := aimap.ClassifierBaseURL(upstream)
		if !ok {
			return nil, c.fail(modelSrc, ConfigFieldInvalid,
				"model %q: target %q of classifier model %q has upstream_url %q. "+
					"A classifier upstream_url must end with %s.",
				m.Name, tm.Name, name, upstream, aimap.ClassifierFormat)
		}
		if problem := aimap.ClassifierBaseURLProblem(base); problem != "" {
			return nil, c.fail(modelSrc, ConfigFieldInvalid,
				"model %q: the data plane rejects the classifier base URL %q of classifier model %q: %s",
				m.Name, base, name, problem)
		}
		out["type"] = "custom"
		out["url"] = base
		out["format"] = aimap.ClassifierFormat
	} else {
		out["type"] = "model"
		out["provider"] = aimap.TypesafeProviderType
	}
	if auth != nil {
		out["auth"] = auth
	}
	for _, key := range []string{"timeout", "context_window"} {
		if v, ok := block[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

// classifierAuth returns the auth header of the classifier provider. The
// data plane sends one classifier header and no other credential.
func (c *Converter) classifierAuth(
	src ConversionIssueSource, m *aigw.Model, classifier string, provider *aigw.Provider,
) (map[string]any, error) {
	if provider.TokenVault != nil {
		return nil, c.fail(src, ConfigFieldInvalid,
			"model %q: provider %q of classifier model %q uses a token vault. "+
				"The classifier sends a fixed auth header only.",
			m.Name, provider.Name, classifier)
	}
	auth := resolveAuth(provider, nil)
	var other []string
	for _, key := range slices.Sorted(maps.Keys(auth)) {
		if key != "header_name" && key != "header_value" {
			other = append(other, key)
		}
	}
	if len(other) > 0 {
		return nil, c.fail(src, ConfigFieldInvalid,
			"model %q: provider %q of classifier model %q sets auth %s. "+
				"The classifier sends one auth header only.",
			m.Name, provider.Name, classifier, strings.Join(other, ", "))
	}
	return auth, nil
}

func (c *Converter) modelNamed(name string) *aigw.Model {
	for i := range c.src.Models {
		if c.src.Models[i].Name == name {
			return &c.src.Models[i]
		}
	}
	return nil
}

// servesDecisions reports whether m lists the decisions capability and the
// typesafe format, by name or by "*".
func servesDecisions(m *aigw.Model) bool {
	capability := capabilitiesWildcard(m) || slices.Contains(explicitCapabilities(m), "decisions")
	format := formatsWildcard(m) || slices.Contains(explicitFormats(m), aimap.TypesafeProviderType)
	return capability && format
}
