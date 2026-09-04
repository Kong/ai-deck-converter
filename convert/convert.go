// Package convert translates an AI Gateway entity-model document into a Kong
// Gateway decK declarative configuration. The public entry points are Convert
// (YAML in, YAML out) and ConvertDocument (struct in, struct out).
package convert

import (
	"bytes"
	"errors"
	"fmt"

	publicaigw "github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"gopkg.in/yaml.v3"
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
	// OutputMode controls the emitted Kong config flavor.
	// Supported values are "deck" (default) and "db-less".
	OutputMode string `yaml:"output_mode"`
	// ModelSelectorSources targets the ai-model-selector schema that reads
	// config.sources (Kong/kong-ee#20858): models with different selector
	// shapes (body/header/path field) are merged onto one shared route and
	// one ai-model-selector, with each shape as an entry in config.sources.
	// The two schemas are mutually exclusive on the wire — data planes new
	// enough for config.sources don't accept the legacy top-level
	// config.source, and older data planes don't recognize config.sources at
	// all — so this cannot be auto-detected from the input. A nil pointer
	// (the zero value) defaults to true (config.sources); set it to false to
	// keep targeting the legacy schema (config.source, one shape per route)
	// for data planes that don't support config.sources yet.
	ModelSelectorSources *bool `yaml:"model_selector_sources"`
	// PluginInstanceNames stamps a deterministic instance_name on every
	// generated plugin so Kong Manager and the Admin API can tell instances
	// apart. Off by default: it changes generated output, and Konnect folds the
	// converter version into its config-generation version, so flipping the
	// default would force a resync of every AI Gateway cluster.
	PluginInstanceNames bool `yaml:"plugin_instance_names"`
}

func (o Options) withDefaults() Options {
	if o.OutputMode == "" {
		o.OutputMode = "deck"
	}
	if o.ModelSelectorSources == nil {
		o.ModelSelectorSources = boolPtr(true)
	}
	return o
}

// Convert parses an AI Gateway document from YAML and returns Kong decK YAML
// along with any non-fatal warnings.
func Convert(src []byte, opts Options) ([]byte, []string, error) {
	output, _, warnings, err := WithMetadata(src, opts)
	return output, warnings, err
}

// ConversionMetadata identifies the AI Gateway source for generated plugin
// targets. It is sidecar metadata and is never included in the emitted Kong
// configuration.
type ConversionMetadata struct {
	PluginTargets []PluginTargetSource
	Plugins       []GeneratedEntitySource
	Routes        []GeneratedEntitySource
	Services      []GeneratedEntitySource
}

// PluginTargetSource identifies one target in the converted plugin list and
// the source model target and capability that produced it.
type PluginTargetSource struct {
	PluginIndex      int
	Location         string
	TargetIndex      int
	ModelName        string
	ModelTargetIndex int
	Capability       string
	CapabilityLabel  string
}

// GeneratedEntitySource identifies the source API entity for a generated
// plugin, route, or service. FieldPrefix is used for direct field mappings;
// FieldMappings handle generated field names that differ from the API model.
type GeneratedEntitySource struct {
	Index         int
	Location      string
	EntityType    string
	EntityName    string
	FieldPrefix   string
	FieldMappings []FieldMapping
}

type FieldMapping struct {
	GeneratedPrefix string
	SourcePrefix    string
}

// ConversionDiagnostic identifies an API field involved in a conversion
// failure. It is intentionally independent from Kong's validation errors so
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
	var conversionErr *ConversionError
	if errors.As(err, &conversionErr) {
		return conversionErr, true
	}
	return nil, false
}

// WithMetadata parses an AI Gateway document, converts it into a Kong
// configuration, and returns source metadata for generated plugin targets.
func WithMetadata(src []byte, opts Options) ([]byte, *ConversionMetadata, []string, error) {
	doc, err := publicaigw.Parse(src)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing source document: %w", err)
	}
	return convertParsedDocumentWithMetadata(doc, opts)
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
func ConvertDocument(doc *aigw.Document, opts Options) (*kong.Document, []string, error) { //nolint:revive

	c := newConverter(doc, opts.withDefaults())
	if err := c.run(); err != nil {
		return nil, c.warnings, err
	}
	return c.out, c.warnings, nil
}

// ConvertDocumentToDBLessYAML translates a parsed AI Gateway document into the
// flattened db-less YAML payload understood by Kong data planes.
func ConvertDocumentToDBLessYAML(doc *publicaigw.Document, opts Options) ([]byte, []string, error) { //nolint:revive

	typed, warnings, err := convertDocumentToDBLess(doc, opts)
	if err != nil {
		return nil, warnings, err
	}
	data, err := marshalYAML(typed)
	return data, warnings, err
}

func convertParsedDocumentWithMetadata(
	doc *publicaigw.Document,
	opts Options,
) ([]byte, *ConversionMetadata, []string, error) {
	switch opts = opts.withDefaults(); opts.OutputMode {
	case "deck":
		c := newConverter(doc, opts)
		if err := c.run(); err != nil {
			return nil, nil, c.warnings, err
		}
		data, err := marshalYAML(c.out)
		return data, metadataForDeck(c.out), c.warnings, err
	case "db-less":
		c := newConverter(doc, opts)
		if err := c.run(); err != nil {
			return nil, nil, c.warnings, err
		}
		out := c.projectDBLess()
		data, err := marshalYAML(out)
		metadata := metadataForDBLessPlugins(out.Plugins)
		metadataForDBLessEntities(out, metadata)
		return data, metadata, c.warnings, err
	default:
		return nil, nil, nil, fmt.Errorf("invalid output_mode %q (want deck or db-less)", opts.OutputMode)
	}
}

func metadataForDeck(document *kong.Document) *ConversionMetadata {
	metadata := &ConversionMetadata{}
	for index, plugin := range document.Plugins {
		appendPluginMetadata(metadata, index, fmt.Sprintf("plugins[%d]", index), plugin)
	}
	for serviceIndex, service := range document.Services {
		serviceLocation := fmt.Sprintf("services[%d]", serviceIndex)
		if service.Source != nil {
			metadata.Services = append(metadata.Services, generatedEntitySource(serviceIndex, serviceLocation, service.Source))
		}
		for pluginIndex, plugin := range service.Plugins {
			location := fmt.Sprintf("%s.plugins[%d]", serviceLocation, pluginIndex)
			appendPluginMetadata(metadata, pluginIndex, location, plugin)
		}
		for routeIndex, route := range service.Routes {
			routeLocation := fmt.Sprintf("%s.routes[%d]", serviceLocation, routeIndex)
			if route.Source != nil {
				metadata.Routes = append(metadata.Routes, generatedEntitySource(routeIndex, routeLocation, route.Source))
			}
			for pluginIndex, plugin := range route.Plugins {
				location := fmt.Sprintf("%s.plugins[%d]", routeLocation, pluginIndex)
				appendPluginMetadata(metadata, pluginIndex, location, plugin)
			}
		}
	}
	return metadata
}

func metadataForDBLessPlugins(plugins []kong.DBLessPlugin) *ConversionMetadata {
	metadata := &ConversionMetadata{}
	for pluginIndex, plugin := range plugins {
		appendPluginMetadata(metadata, pluginIndex, fmt.Sprintf("plugins[%d]", pluginIndex), kong.Plugin{
			Source:        plugin.Source,
			TargetSources: plugin.TargetSources,
		})
	}
	return metadata
}

func appendPluginMetadata(metadata *ConversionMetadata, index int, location string, plugin kong.Plugin) {
	if plugin.Source != nil {
		metadata.Plugins = append(metadata.Plugins, generatedEntitySource(index, location, plugin.Source))
	}
	for targetIndex, targetSource := range plugin.TargetSources {
		metadata.PluginTargets = append(metadata.PluginTargets, PluginTargetSource{
			PluginIndex:      index,
			Location:         location,
			TargetIndex:      targetIndex,
			ModelName:        targetSource.ModelName,
			ModelTargetIndex: targetSource.ModelTargetIndex,
			Capability:       targetSource.Capability,
			CapabilityLabel:  aimap.CapabilityLabel(targetSource.Capability),
		})
	}
}

func generatedEntitySource(index int, location string, source *kong.Source) GeneratedEntitySource {
	result := GeneratedEntitySource{
		Index:       index,
		Location:    location,
		EntityType:  source.EntityType,
		EntityName:  source.EntityName,
		FieldPrefix: source.FieldPrefix,
	}
	for _, mapping := range source.FieldMappings {
		result.FieldMappings = append(result.FieldMappings, FieldMapping{
			GeneratedPrefix: mapping.GeneratedPrefix,
			SourcePrefix:    mapping.SourcePrefix,
		})
	}
	return result
}

func metadataForDBLessEntities(document *kong.DBLessDocument, metadata *ConversionMetadata) {
	for index, route := range document.Routes {
		if route.Source != nil {
			metadata.Routes = append(metadata.Routes, generatedEntitySource(
				index, fmt.Sprintf("routes[%d]", index), route.Source,
			))
		}
	}
	for index, service := range document.Services {
		if service.Source != nil {
			metadata.Services = append(metadata.Services, generatedEntitySource(
				index, fmt.Sprintf("services[%d]", index), service.Source,
			))
		}
	}
}

func convertDocumentToDBLess(doc *publicaigw.Document, opts Options) (any, []string, error) {
	c := newConverter(doc, opts.withDefaults())
	if err := c.run(); err != nil {
		return nil, c.warnings, err
	}
	return c.projectDBLess(), c.warnings, nil
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

func buildModelRoute(rc aigw.ModelRouteConfig, routeName string, paths []string, defaultMethods []string) kong.Route {
	route := buildRoute(aigw.RouteConfig{
		Name:                    rc.Name,
		Paths:                   rc.Paths,
		Hosts:                   rc.Hosts,
		Methods:                 rc.Methods,
		Protocols:               rc.Protocols,
		Headers:                 rc.Headers,
		SNIs:                    rc.SNIs,
		Sources:                 rc.Sources,
		Destinations:            rc.Destinations,
		StripPath:               rc.StripPath,
		PreserveHost:            rc.PreserveHost,
		HTTPSRedirectStatusCode: rc.HTTPSRedirectStatusCode,
		RegexPriority:           rc.RegexPriority,
		PathHandling:            rc.PathHandling,
		RequestBuffering:        rc.RequestBuffering,
		ResponseBuffering:       rc.ResponseBuffering,
		Tags:                    rc.Tags,
	}, routeName)
	route.Name = routeName
	route.Paths = paths
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
	if err := c.convertMCPServers(); err != nil {
		return err
	}
	if err := c.convertAgents(); err != nil {
		return err
	}
	if c.opts.PluginInstanceNames {
		if err := applyPluginInstanceNames(c.out); err != nil {
			return err
		}
	}
	return nil
}
