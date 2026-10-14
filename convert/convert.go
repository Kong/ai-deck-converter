// Package convert translates an AI Gateway entity-model document into a Kong
// Gateway decK declarative configuration. The public entry point is Convert.
package convert

import (
	"fmt"

	publicaigw "github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// placeholderHost is used for synthetic Services (e.g. MCP servers without an
// explicit upstream URL) where decK still requires a host.
const placeholderHost = "localhost"

// Options controls conversion behavior.
type Options struct {
	// LabelTagPrefix is prepended to label-derived tags, e.g. "aigw/".
	LabelTagPrefix string `yaml:"label_tag_prefix"`
}

// Convert parses a YAML AI Gateway document and returns the Kong decK document
// with every issue found during conversion.
// It returns an error only when the whole run fails, for example on invalid
// YAML. Use the output's ToYAML and ToDBLess methods to encode it.
func Convert(src []byte, opts Options) (*ConversionResult, error) {
	doc, err := publicaigw.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parsing source document: %w", err)
	}

	c := newConverter(doc, opts)
	c.run()
	return c.result(), nil
}

// These aliases keep the types nameable outside the module.
type (
	ConversionMetadata    = kong.ConversionMetadata
	PluginTargetSource    = kong.PluginTargetSource
	GeneratedEntitySource = kong.GeneratedEntitySource
	FieldMapping          = kong.FieldMapping
	Document              = kong.Document
	DBLessDocument        = kong.DBLessDocument
)

// Converter holds conversion state: source registries, the output document, and
// the issues found so far.
type Converter struct {
	opts Options
	src  *aigw.Document
	out  *kong.Document

	providers      map[string]*aigw.Provider
	policies       map[string]*aigw.Policy
	authStrategies map[string]*aigw.AuthStrategy
	consumerGroups map[string]*aigw.ConsumerGroup
	datastores     map[string]*aigw.Datastore

	issues []ConversionIssue

	// beforeEntity is a test hook. guard calls it before each entity.
	beforeEntity func(ConversionIssueSource)
}

func newConverter(doc *aigw.Document, opts Options) *Converter {
	return &Converter{
		opts:           opts,
		src:            doc,
		out:            kong.NewDocument(),
		providers:      map[string]*aigw.Provider{},
		policies:       map[string]*aigw.Policy{},
		authStrategies: map[string]*aigw.AuthStrategy{},
		consumerGroups: map[string]*aigw.ConsumerGroup{},
		datastores:     map[string]*aigw.Datastore{},
	}
}

func (c *Converter) result() *ConversionResult {
	return &ConversionResult{
		Output:   c.out,
		Metadata: *c.out.Metadata(),
		Issues:   c.issues,
	}
}

func (c *Converter) run() {
	c.buildRegistries()
	c.convertGlobalPolicies()
	c.convertCustomPolicies()
	c.convertVaults()
	c.convertCACertificates()
	c.convertCertificates()
	c.convertSNIs()
	c.convertConsumerGroups()
	c.convertConsumers()
	c.convertModels()
	c.warnGlobalPoliciesForWebSocket()
	c.convertMCPServers()
	c.convertAgents()
}
