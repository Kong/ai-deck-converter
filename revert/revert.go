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

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"gopkg.in/yaml.v3"
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
func RevertDocument(doc *kong.Document, opts Options) (*aigw.Document, []string, error) { //nolint:revive

	r := newReverter(doc, opts)
	if err := r.run(); err != nil {
		return nil, r.warnings, err
	}
	assignDisplayNames(r.out)
	assignRefs(r.out)
	return r.out, r.warnings, nil
}

// assignRefs fills in the `ref` identifier kongctl requires on every AI Gateway
// entity. Kong has no ref concept, so reversal synthesizes it from the entity's
// name (a consumer with no name falls back to its custom_id). The forward
// converter never reads ref, so this does not affect round trips.
func assignRefs(doc *aigw.Document) {
	for i := range doc.Models {
		if doc.Models[i].Ref == "" {
			doc.Models[i].Ref = doc.Models[i].Name
		}
	}
	for i := range doc.ModelProviders {
		if doc.ModelProviders[i].Ref == "" {
			doc.ModelProviders[i].Ref = doc.ModelProviders[i].Name
		}
	}
	for i := range doc.Policies {
		if doc.Policies[i].Ref == "" {
			doc.Policies[i].Ref = doc.Policies[i].Name
		}
	}
	for i := range doc.Agents {
		if doc.Agents[i].Ref == "" {
			doc.Agents[i].Ref = doc.Agents[i].Name
		}
	}
	for i := range doc.ConsumerGroups {
		if doc.ConsumerGroups[i].Ref == "" {
			doc.ConsumerGroups[i].Ref = doc.ConsumerGroups[i].Name
		}
	}
	for i := range doc.MCPServers {
		if doc.MCPServers[i].Ref == "" {
			doc.MCPServers[i].Ref = doc.MCPServers[i].Name
		}
	}
	for i := range doc.IdentityProviders {
		if doc.IdentityProviders[i].Ref == "" {
			doc.IdentityProviders[i].Ref = doc.IdentityProviders[i].Name
		}
	}
	for i := range doc.Vaults {
		if doc.Vaults[i].Ref == "" {
			doc.Vaults[i].Ref = doc.Vaults[i].Name
		}
	}
	for i := range doc.Consumers {
		c := &doc.Consumers[i]
		if c.Ref != "" {
			continue
		}
		if c.Name != "" {
			c.Ref = c.Name
		} else {
			c.Ref = c.CustomID
		}
	}
	for i := range doc.Consumers {
		for j := range doc.Consumers[i].Credentials {
			cred := &doc.Consumers[i].Credentials[j]
			if cred.Ref == "" {
				cred.Ref = cred.Name
			}
		}
	}
}

// assignDisplayNames fills in the display_name required by the AI Gateway schema
// on every entity that lacks one. Kong has no display-name concept, so the
// forward direction drops it (lossy by design) and reversal must synthesize it:
// display_name mirrors the entity's name. Consumer credentials additionally have
// no name in decK (key-auth credentials carry only a key), so a deterministic
// name is synthesized from the owning consumer. The forward converter never
// reads display_name or a credential's name, so this does not affect round trips.
// Vaults are Kong-core entities outside the AI Gateway schema and are skipped.
func assignDisplayNames(doc *aigw.Document) {
	for i := range doc.Models {
		if doc.Models[i].DisplayName == "" {
			doc.Models[i].DisplayName = doc.Models[i].Name
		}
	}
	for i := range doc.ModelProviders {
		if doc.ModelProviders[i].DisplayName == "" {
			doc.ModelProviders[i].DisplayName = doc.ModelProviders[i].Name
		}
	}
	for i := range doc.Policies {
		if doc.Policies[i].DisplayName == "" {
			doc.Policies[i].DisplayName = doc.Policies[i].Name
		}
	}
	for i := range doc.Agents {
		if doc.Agents[i].DisplayName == "" {
			doc.Agents[i].DisplayName = doc.Agents[i].Name
		}
	}
	for i := range doc.ConsumerGroups {
		if doc.ConsumerGroups[i].DisplayName == "" {
			doc.ConsumerGroups[i].DisplayName = doc.ConsumerGroups[i].Name
		}
	}
	for i := range doc.MCPServers {
		if doc.MCPServers[i].DisplayName == "" {
			doc.MCPServers[i].DisplayName = doc.MCPServers[i].Name
		}
	}
	for i := range doc.IdentityProviders {
		if doc.IdentityProviders[i].DisplayName == "" {
			doc.IdentityProviders[i].DisplayName = doc.IdentityProviders[i].Name
		}
	}
	for i := range doc.Consumers {
		c := &doc.Consumers[i]
		if c.DisplayName == "" {
			c.DisplayName = c.Name
		}
		for j := range c.Credentials {
			cred := &c.Credentials[j]
			if cred.Name == "" {
				cred.Name = fmt.Sprintf("%s-credential-%d", c.Name, j+1)
			}
			if cred.DisplayName == "" {
				cred.DisplayName = cred.Name
			}
		}
	}
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
	aiModelByName  map[string]kong.AIModel
	aiModelUsed    map[string]bool

	// synthesized providers, deduped by fingerprint.
	providers      []aigw.Provider
	providerByFP   map[string]string // fingerprint -> provider name
	providerNames  map[string]bool
	providerCounts map[string]int // provider type -> running index

	// policies recovered from plugins, deduped by (type, config, enabled).
	policies    []aigw.Policy
	policyNames map[string]bool

	// identity providers recovered from key-auth/openid-connect plugins, deduped
	// by (type, config-without-anonymous) fingerprint.
	identityProviders      []aigw.IdentityProvider
	identityProviderByFP   map[string]string // fingerprint -> identity provider name
	identityProviderNames  map[string]bool
	identityProviderCounts map[string]int // identity provider type -> running index

	// MCP server names already emitted, to uniquify collisions when several
	// routes of one service each become an MCP server.
	mcpNames map[string]bool

	// usedNames is the union of names assigned to synthesized entities
	// (policies, identity providers, providers). kongctl requires refs to be
	// globally unique across entity types, so these registries share it to
	// avoid a policy and an identity provider both taking e.g.
	// "openid-connect-2".
	usedNames map[string]bool

	warnings []string
}

func newReverter(doc *kong.Document, opts Options) *Reverter {
	return &Reverter{
		opts:                   opts,
		src:                    doc,
		out:                    &aigw.Document{},
		aiModelByAlias:         map[string]string{},
		aiModelByName:          map[string]kong.AIModel{},
		aiModelUsed:            map[string]bool{},
		providerByFP:           map[string]string{},
		providerNames:          map[string]bool{},
		providerCounts:         map[string]int{},
		policyNames:            map[string]bool{},
		identityProviderByFP:   map[string]string{},
		identityProviderNames:  map[string]bool{},
		identityProviderCounts: map[string]int{},
		mcpNames:               map[string]bool{},
		usedNames:              map[string]bool{},
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
	r.out.ModelProviders = r.providers
	r.out.Policies = r.policies
	r.out.IdentityProviders = r.identityProviders
	return nil
}

// tagsToLabels converts tag strings back into a label map; tags that do not
// look like converted labels are dropped.
func (r *Reverter) tagsToLabels(tags []string) aigw.Labels {
	labels, _ := aimap.TagsToLabels(tags, r.opts.LabelTagPrefix)
	return labels
}
