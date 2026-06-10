// Package convert translates an AI Gateway entity-model document into a Kong
// Gateway decK declarative configuration. The public entry points are Convert
// (YAML in, YAML out) and ConvertDocument (struct in, struct out).
package convert

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/gperanich/ai-deck-converter/internal/aigw"
	"github.com/gperanich/ai-deck-converter/internal/kong"
)

// yamlIndent is the indentation (in spaces) used for the emitted decK YAML.
// Two spaces matches the conventional Kong/decK layout, with nested mappings and
// sequence items each indented one level under their parent.
const yamlIndent = 2

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

func (o Options) withDefaults() Options {
	return o
}

// Convert parses an AI Gateway document from YAML and returns Kong decK YAML
// along with any non-fatal warnings.
func Convert(src []byte, opts Options) ([]byte, []string, error) {
	doc, err := aigw.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing source document: %w", err)
	}
	out, warnings, err := ConvertDocument(doc, opts)
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

// ConvertDocument translates a parsed AI Gateway document into a Kong decK
// document, returning collected warnings. Unresolved references are warnings
// unless Options.Strict is set, in which case they become errors.
func ConvertDocument(doc *aigw.Document, opts Options) (*kong.Document, []string, error) {
	c := newConverter(doc, opts.withDefaults())
	if err := c.run(); err != nil {
		return nil, c.warnings, err
	}
	return c.out, c.warnings, nil
}

// Converter holds conversion state: source registries, the output document, and
// accumulated warnings.
type Converter struct {
	opts Options
	src  *aigw.Document
	out  *kong.Document

	providers      map[string]*aigw.Provider
	policies       map[string]*aigw.Policy
	consumerGroups map[string]*aigw.ConsumerGroup

	warnings []string
}

func newConverter(doc *aigw.Document, opts Options) *Converter {
	return &Converter{
		opts:           opts,
		src:            doc,
		out:            kong.NewDocument(),
		providers:      map[string]*aigw.Provider{},
		policies:       map[string]*aigw.Policy{},
		consumerGroups: map[string]*aigw.ConsumerGroup{},
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

// buildRoute converts an AI Gateway route config into a Kong Route (used by MCP
// servers and agents, which route from their own config.route).
func buildRoute(rc aigw.RouteConfig, entityName string) kong.Route {
	name := rc.Name
	if name == "" {
		name = entityName + "-route"
	}
	return kong.Route{
		Name:                    name,
		Paths:                   rc.Paths,
		Hosts:                   rc.Hosts,
		Methods:                 rc.Methods,
		Protocols:               rc.Protocols,
		Headers:                 rc.Headers,
		SNIs:                    cloneStrings(rc.SNIs),
		Sources:                 toKongCIDRPorts(rc.Sources),
		Destinations:            toKongCIDRPorts(rc.Destinations),
		StripPath:               rc.StripPath,
		PreserveHost:            rc.PreserveHost,
		HTTPSRedirectStatusCode: rc.HTTPSRedirectStatusCode,
		RegexPriority:           rc.RegexPriority,
		PathHandling:            rc.PathHandling,
		RequestBuffering:        rc.RequestBuffering,
		ResponseBuffering:       rc.ResponseBuffering,
		Tags:                    rc.Tags,
	}
}

func buildModelRoute(rc aigw.RouteConfig, routeName, path string, defaultMethods []string) kong.Route {
	route := buildRoute(rc, routeName)
	route.Name = routeName
	route.Paths = []string{path}
	if len(route.Methods) == 0 {
		route.Methods = defaultMethods
	}
	if route.StripPath == nil {
		route.StripPath = boolPtr(false)
	}
	return route
}

func toKongCIDRPorts(in []aigw.CIDRPort) []kong.CIDRPort {
	if len(in) == 0 {
		return nil
	}
	out := make([]kong.CIDRPort, 0, len(in))
	for _, item := range in {
		out = append(out, kong.CIDRPort{
			IP:   item.IP,
			Port: item.Port,
		})
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (c *Converter) run() error {
	c.buildRegistries()
	c.convertGlobalPolicies()
	c.convertVaults()
	if err := c.convertConsumerGroups(); err != nil {
		return err
	}
	if err := c.convertConsumers(); err != nil {
		return err
	}
	if err := c.convertModels(); err != nil {
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
