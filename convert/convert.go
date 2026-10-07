// Package convert translates an AI Gateway entity-model document into a Kong
// Gateway decK declarative configuration. The public entry point is Convert.
package convert

import (
	"errors"
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
	// Strict makes unresolved references (unknown provider/policy) fatal instead
	// of warnings.
	Strict bool `yaml:"strict"`
	// LabelTagPrefix is prepended to label-derived tags, e.g. "aigw/".
	LabelTagPrefix string `yaml:"label_tag_prefix"`
}

// Convert parses a YAML AI Gateway document and returns the Kong decK document
// along with any non-fatal warnings.
// Use the document's ToYAML, ToDBLess, and Metadata methods for output.
func Convert(src []byte, opts Options) (*kong.Document, []string, error) {
	doc, err := publicaigw.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing source document: %w", err)
	}

	c := newConverter(doc, opts)
	if err := c.run(); err != nil {
		return nil, c.warnings, err
	}
	return c.out, c.warnings, nil
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

// ConversionDiagnostic identifies an API field involved in a conversion
// failure. It is intentionally independent of Kong's validation errors, so
// converter callers can return source-native diagnostics without parsing text.
type ConversionDiagnostic struct {
	Field    string
	Messages []string
}

// ConversionError is returned for source configurations that cannot be
// represented safely as Kong entities.
type ConversionError struct {
	Diagnostics []ConversionDiagnostic
}

func (e *ConversionError) Error() string {
	if len(e.Diagnostics) == 0 || len(e.Diagnostics[0].Messages) == 0 {
		return "AI Gateway configuration cannot be converted"
	}
	return e.Diagnostics[0].Messages[0]
}

func (c *Converter) failAt(field, format string, args ...any) error {
	return &ConversionError{Diagnostics: []ConversionDiagnostic{{
		Field:    field,
		Messages: []string{fmt.Sprintf(format, args...)},
	}}}
}

// AsConversionError unwraps a converter error while preserving the public
// error type behind any contextual wrappers added by callers.
func AsConversionError(err error) (*ConversionError, bool) {
	if conversionErr, ok := errors.AsType[*ConversionError](err); ok {
		return conversionErr, true
	}
	return nil, false
}

// Converter holds conversion state: source registries, the output document, and
// accumulated warnings.
type Converter struct {
	opts Options
	src  *aigw.Document
	out  *kong.Document

	providers      map[string]*aigw.Provider
	policies       map[string]*aigw.Policy
	authStrategies map[string]*aigw.AuthStrategy
	consumerGroups map[string]*aigw.ConsumerGroup
	datastores     map[string]*aigw.Datastore

	warnings []string
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

// Warnings returns the warnings collected during conversion.
func (c *Converter) Warnings() []string { return c.warnings }

func (c *Converter) warn(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if c.opts.Strict {
		return fmt.Errorf("%s", msg)
	}
	c.warnings = append(c.warnings, msg)
	return nil
}

func (c *Converter) run() error {
	c.buildRegistries()
	if err := c.convertGlobalPolicies(); err != nil {
		return err
	}
	if err := c.convertCustomPolicies(); err != nil {
		return err
	}
	c.convertVaults()
	c.convertCACertificates()
	c.convertCertificates()
	if err := c.convertSNIs(); err != nil {
		return err
	}
	if err := c.convertConsumerGroups(); err != nil {
		return err
	}
	if err := c.convertConsumers(); err != nil {
		return err
	}
	if err := c.convertModels(); err != nil {
		return err
	}
	if err := c.warnGlobalPoliciesForWebSocket(); err != nil {
		return err
	}
	if err := c.convertMCPServers(); err != nil {
		return err
	}
	if err := c.convertAgents(); err != nil {
		return err
	}
	return nil
}
