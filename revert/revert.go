// Package revert translates a Kong Gateway decK declarative configuration back
// into an AI Gateway entity-model document — the inverse of package convert.
// The public entry points are Revert (YAML in, YAML out) and RevertDocument
// (struct in, struct out).
//
// Reversal is best-effort: it recognizes the AI plugins (ai-proxy-advanced,
// ai-model-selector, ai-mcp-proxy, ai-a2a-proxy) anywhere in the config, and
// uses the forward converter's conventions (the shared ai-gateway service,
// "{section}-{label}" route names, ai-models entries) as hints when present.
// Kong entities with no AI Gateway representation are converted where sensible
// (plain HTTP services become http agents, unknown plugins become policies)
// and warned about otherwise.
package revert

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// yamlIndent is the indentation (in spaces) used for the emitted YAML.
const yamlIndent = 2

// placeholderHost is the synthetic host the forward converter uses for MCP
// Services without an explicit upstream URL; reversed back to "no upstream".
const placeholderHost = "localhost"

// Options controls reversal behavior.
type Options struct {
	// Strict makes unconvertible entities (unresolvable routes, dropped
	// plugins) fatal instead of warnings.
	Strict bool `yaml:"strict"`
	// LabelTagPrefix is stripped from tag-derived labels, e.g. "aigw/". Tags
	// without the prefix (or without a ":" separator) are not converted.
	LabelTagPrefix string `yaml:"label_tag_prefix"`
}

// Revert parses a Kong decK document from YAML and returns AI Gateway
// entity-model YAML along with any non-fatal warnings.
func Revert(src []byte, opts Options) ([]byte, []string, error) {
	var doc kong.Document
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing decK document: %w", err)
	}
	out, warnings, err := RevertDocument(&doc, opts)
	if err != nil {
		return nil, warnings, err
	}
	data, err := marshalYAML(out)
	return data, warnings, err
}

// marshalYAML encodes v as YAML using a fixed two-space indent.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RevertDocument translates a parsed Kong decK document into an AI Gateway
// document, returning collected warnings. Unconvertible entities are warnings
// unless Options.Strict is set, in which case they become errors.
func RevertDocument(doc *kong.Document, opts Options) (*aigw.Document, []string, error) {
	r := newReverter(doc, opts)
	if err := r.run(); err != nil {
		return nil, r.warnings, err
	}
	return r.out, r.warnings, nil
}

// Reverter holds reversal state: plugin/ai-model indexes built from the source
// document, accumulator registries for synthesized providers and policies, the
// output document, and accumulated warnings.
type Reverter struct {
	opts Options
	src  *kong.Document
	out  *aigw.Document

	idx pluginIndex

	// ai-models entries indexed by alias and name, with usage tracking so
	// orphans can be reported.
	aiModelByAlias map[string]string
	aiModelUsed    map[string]bool

	// synthesized providers, deduped by fingerprint.
	providers      []aigw.Provider
	providerByFP   map[string]string // fingerprint -> provider name
	providerNames  map[string]bool
	providerCounts map[string]int // provider type -> running index

	// policies recovered from plugins, deduped by (type, config, enabled).
	policies    []aigw.Policy
	policyNames map[string]bool

	warnings []string
}

func newReverter(doc *kong.Document, opts Options) *Reverter {
	return &Reverter{
		opts:           opts,
		src:            doc,
		out:            &aigw.Document{},
		aiModelByAlias: map[string]string{},
		aiModelUsed:    map[string]bool{},
		providerByFP:   map[string]string{},
		providerNames:  map[string]bool{},
		providerCounts: map[string]int{},
		policyNames:    map[string]bool{},
	}
}

// Warnings returns the warnings collected during reversal.
func (r *Reverter) Warnings() []string { return r.warnings }

func (r *Reverter) warn(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if r.opts.Strict {
		return fmt.Errorf("%s", msg)
	}
	r.warnings = append(r.warnings, msg)
	return nil
}

func (r *Reverter) run() error {
	r.buildIndexes()
	r.revertGlobalPolicies()
	r.revertVaults()
	if err := r.revertConsumerGroups(); err != nil {
		return err
	}
	if err := r.revertConsumers(); err != nil {
		return err
	}
	if err := r.revertServices(); err != nil {
		return err
	}
	r.out.Providers = r.providers
	r.out.Policies = r.policies
	return nil
}

// tagsToLabels converts tag strings back into a label map; tags that do not
// look like converted labels are dropped.
func (r *Reverter) tagsToLabels(tags []string) aigw.Labels {
	labels, _ := aimap.TagsToLabels(tags, r.opts.LabelTagPrefix)
	return labels
}

func boolPtr(b bool) *bool { return &b }
