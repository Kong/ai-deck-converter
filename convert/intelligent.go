package convert

import (
	"maps"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

const classifierField = "config.balancer.classifier"

// classifierKeys are the fields a balancer.classifier block can hold.
var classifierKeys = []string{"auth", "context_window", "format", "name", "timeout", "type", "url"}

// classifierRequired are the classifier fields that the API requires. The
// second value goes in the issue reason.
var classifierRequired = [][2]string{
	{"type", `set to "custom"`},
	{"url", "the base URL of the classifier"},
	{"name", "the classifier model name"},
	{"context_window", "the number of tokens the classifier reads"},
}

// classifierAuthKeys are the fields a classifier auth block can hold.
var classifierAuthKeys = []string{"headers", "type"}

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
// classifier record.
func (c *Converter) classifierConfig(src ConversionIssueSource, m *aigw.Model, raw any) (map[string]any, error) {
	if raw == nil {
		return nil, c.fail(src.at(classifierField), MissingRequiredConfigField,
			"model %q: the intelligent balancer needs %s", m.Name, classifierField)
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, c.fail(src.at(classifierField), ConfigFieldInvalid,
			"model %q: %s is not a map", m.Name, classifierField)
	}
	if key, ok := unknownKey(block, classifierKeys); ok {
		return nil, c.fail(src.at(classifierField+"."+key), ConfigFieldInvalid,
			"model %q: %s.%s is not a classifier field. The fields are %s.",
			m.Name, classifierField, key, strings.Join(classifierKeys, ", "))
	}
	for _, field := range classifierRequired {
		if _, ok := block[field[0]]; !ok {
			return nil, c.fail(src.at(classifierField+"."+field[0]), MissingRequiredConfigField,
				"model %q: the intelligent balancer needs %s.%s, %s",
				m.Name, classifierField, field[0], field[1])
		}
	}
	if block["type"] != aimap.ClassifierTypeCustom {
		return nil, c.fail(src.at(classifierField+".type"), ConfigFieldInvalid,
			"model %q: classifier type %#v is not supported. The only type is %q.",
			m.Name, block["type"], aimap.ClassifierTypeCustom)
	}
	url, _ := block["url"].(string)
	if problem := aimap.ClassifierURLProblem(url); problem != "" {
		return nil, c.fail(src.at(classifierField+".url"), ConfigFieldInvalid,
			"model %q: the data plane rejects classifier url %#v: %s", m.Name, block["url"], problem)
	}
	name, _ := block["name"].(string)
	if name == "" {
		return nil, c.fail(src.at(classifierField+".name"), ConfigFieldInvalid,
			"model %q: %s.name is empty or not a string", m.Name, classifierField)
	}
	format := aimap.ClassifierFormat
	if v, ok := block["format"]; ok {
		if format, _ = v.(string); format == "" {
			return nil, c.fail(src.at(classifierField+".format"), ConfigFieldInvalid,
				"model %q: %s.format is empty or not a string", m.Name, classifierField)
		}
	}

	out := map[string]any{
		"type":           aimap.ClassifierTypeCustom,
		"url":            url,
		"format":         format,
		"name":           name,
		"context_window": block["context_window"],
	}
	if v, ok := block["timeout"]; ok {
		out["timeout"] = v
	}
	if v, ok := block["auth"]; ok {
		auth, err := c.classifierAuth(src, m, v)
		if err != nil {
			return nil, err
		}
		if auth != nil {
			out["auth"] = auth
		}
	}
	return out, nil
}

// classifierAuth changes a classifier auth block to the data plane auth
// record. The data plane sends one classifier header and no other credential.
func (c *Converter) classifierAuth(src ConversionIssueSource, m *aigw.Model, raw any) (map[string]any, error) {
	const field = classifierField + ".auth"
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, c.fail(src.at(field), ConfigFieldInvalid, "model %q: %s is not a map", m.Name, field)
	}
	if key, ok := unknownKey(block, classifierAuthKeys); ok {
		return nil, c.fail(src.at(field+"."+key), ConfigFieldInvalid,
			"model %q: %s.%s is not supported. The classifier sends one auth header only.",
			m.Name, field, key)
	}
	if block["type"] != "basic" {
		return nil, c.fail(src.at(field+".type"), ConfigFieldInvalid,
			"model %q: classifier auth type %#v is not supported. Use \"basic\".", m.Name, block["type"])
	}
	rawHeaders, ok := block["headers"]
	if !ok {
		return nil, nil
	}
	headers, ok := rawHeaders.([]any)
	switch {
	case !ok:
		return nil, c.fail(src.at(field+".headers"), ConfigFieldInvalid,
			"model %q: %s.headers is not a list", m.Name, field)
	case len(headers) == 0:
		return nil, nil
	case len(headers) > 1:
		return nil, c.fail(src.at(field+".headers"), ConfigFieldInvalid,
			"model %q: %s.headers has %d entries. The classifier sends one auth header only.",
			m.Name, field, len(headers))
	}
	header, _ := headers[0].(map[string]any)
	for _, key := range []string{"name", "value"} {
		if v, _ := header[key].(string); v == "" {
			return nil, c.fail(src.at(field+".headers[0]."+key), MissingRequiredConfigField,
				"model %q: the classifier auth header needs a %s. The data plane needs both name and value.",
				m.Name, key)
		}
	}
	return map[string]any{"header_name": header["name"], "header_value": header["value"]}, nil
}

// unknownKey returns the first key of block, in sorted order, that keys does
// not hold.
func unknownKey(block map[string]any, keys []string) (string, bool) {
	for _, key := range slices.Sorted(maps.Keys(block)) {
		if !slices.Contains(keys, key) {
			return key, true
		}
	}
	return "", false
}
