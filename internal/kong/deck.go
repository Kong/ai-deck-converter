// Package kong defines the Kong Gateway decK declarative configuration types
// that the converter emits. These structs are shaped to marshal directly into
// the YAML decK expects (_format_version, nested routes/plugins, name-based
// foreign-key references).
package kong

// FormatVersion is the decK declarative config version this converter targets.
const FormatVersion = "3.0"

// Document is the top-level decK declarative configuration document.
type Document struct {
	FormatVersion  string          `yaml:"_format_version"`
	Services       []Service       `yaml:"services,omitempty"`
	Consumers      []Consumer      `yaml:"consumers,omitempty"`
	ConsumerGroups []ConsumerGroup `yaml:"consumer_groups,omitempty"`
	Plugins        []Plugin        `yaml:"plugins,omitempty"`
	Vaults         []Vault         `yaml:"vaults,omitempty"`
	Certificates   []Certificate   `yaml:"certificates,omitempty"`
	AIModels       []AIModel       `yaml:"ai_models,omitempty"`
	CACertificates []CACertificate `yaml:"ca_certificates,omitempty"`
}

// Ref is a name-based foreign-key reference, rendered as `{name: <x>}`.
type Ref struct {
	Name string `yaml:"name"`
}

// UnmarshalYAML handles both string and {name: <x>} formats.
func (r *Ref) UnmarshalYAML(unmarshal func(any) error) error {
	// Try string first
	var s string
	stringErr := unmarshal(&s)
	if stringErr == nil {
		r.Name = s
		return nil
	}

	// Try to unmarshal as an object with a name field
	var obj struct {
		Name string `yaml:"name"`
	}
	if err := unmarshal(&obj); err == nil && obj.Name != "" {
		r.Name = obj.Name
		return nil
	}

	// Return the original string unmarshal error if both failed
	return stringErr
}

// Ref is a name-based foreign-key reference, rendered as `{name: <x>}`.
type StringRef string

// NewRef returns a pointer to a Ref with the given name.
func NewRef(name string) *Ref { return &Ref{Name: name} }

// NewStringRef returns a pointer to a StringRef with the given name.
func NewStringRef(name string) *StringRef { ref := StringRef(name); return &ref }

// UnmarshalYAML handles both string and {name: <x>} formats.
func (sr *StringRef) UnmarshalYAML(unmarshal func(any) error) error {
	// Try string first
	var s string
	stringErr := unmarshal(&s)
	if stringErr == nil {
		*sr = StringRef(s)
		return nil
	}

	// Try to unmarshal as an object with a name field (dbless format)
	var obj struct {
		Name string `yaml:"name"`
	}
	if err := unmarshal(&obj); err == nil && obj.Name != "" {
		*sr = StringRef(obj.Name)
		return nil
	}

	// Return the original string unmarshal error if both failed
	return stringErr
}

// NewDocument returns an empty document with the format version set.
func NewDocument() *Document {
	return &Document{FormatVersion: FormatVersion}
}

// Service is a Kong Gateway Service. Routes and Plugins may be nested.
type Service struct {
	Name     string   `yaml:"name"`
	ID       string   `yaml:"id,omitempty"`
	URL      string   `yaml:"url,omitempty"`
	Host     string   `yaml:"host,omitempty"`
	Port     *int     `yaml:"port,omitempty"`
	Protocol string   `yaml:"protocol,omitempty"`
	Path     string   `yaml:"path,omitempty"`
	Enabled  *bool    `yaml:"enabled,omitempty"`
	Retries  *int     `yaml:"retries,omitempty"`
	Routes   []Route  `yaml:"routes,omitempty"`
	Plugins  []Plugin `yaml:"plugins,omitempty"`
	Tags     []string `yaml:"tags,omitempty"`
	Source   *Source  `yaml:"-"`
}

// Route is a Kong Gateway Route. Plugins may be nested.
type Route struct {
	Name                    string              `yaml:"name"`
	Paths                   []string            `yaml:"paths,omitempty"`
	Hosts                   []string            `yaml:"hosts,omitempty"`
	Methods                 []string            `yaml:"methods,omitempty"`
	Protocols               []string            `yaml:"protocols,omitempty"`
	Headers                 map[string][]string `yaml:"headers,omitempty"`
	SNIs                    []string            `yaml:"snis,omitempty"`
	Sources                 []CIDRPort          `yaml:"sources,omitempty"`
	Destinations            []CIDRPort          `yaml:"destinations,omitempty"`
	StripPath               *bool               `yaml:"strip_path,omitempty"`
	PreserveHost            *bool               `yaml:"preserve_host,omitempty"`
	HTTPSRedirectStatusCode *int                `yaml:"https_redirect_status_code,omitempty"`
	RegexPriority           *int                `yaml:"regex_priority,omitempty"`
	PathHandling            string              `yaml:"path_handling,omitempty"`
	RequestBuffering        *bool               `yaml:"request_buffering,omitempty"`
	ResponseBuffering       *bool               `yaml:"response_buffering,omitempty"`
	Plugins                 []Plugin            `yaml:"plugins,omitempty"`
	Tags                    []string            `yaml:"tags,omitempty"`
	Source                  *Source             `yaml:"-"`
}

// CIDRPort mirrors Kong route source/destination entries.
type CIDRPort struct {
	IP   string `yaml:"ip,omitempty"`
	Port *int   `yaml:"port,omitempty"`
}

// Plugin is a Kong Gateway Plugin. When emitted at the top level, the foreign-key
// reference fields name the entity it is scoped to (rendered as `{name: <x>}`).
// When nested under an entity, those fields are left nil.
type Plugin struct {
	ID            string         `yaml:"id,omitempty"`
	Name          string         `yaml:"name"`
	InstanceName  string         `yaml:"instance_name,omitempty"`
	Enabled       *bool          `yaml:"enabled,omitempty"`
	Config        map[string]any `yaml:"config,omitempty"`
	Service       *StringRef     `yaml:"service,omitempty"`
	Route         *StringRef     `yaml:"route,omitempty"`
	Consumer      *StringRef     `yaml:"consumer,omitempty"`
	ConsumerGroup *StringRef     `yaml:"consumer_group,omitempty"`
	Model         *StringRef     `yaml:"model,omitempty"`
	Tags          []string       `yaml:"tags,omitempty"`
	TargetSources []TargetSource `yaml:"-"`
	Source        *Source        `yaml:"-"`
}

// Source identifies the original AI Gateway entity field which produced a
// generated Kong entity. It is conversion-only metadata and never emitted.
// FieldMappings translate fields whose generated names or nesting differ from
// the source API model.
type Source struct {
	EntityType    string
	EntityName    string
	FieldPrefix   string
	FieldMappings []FieldMapping
}

type FieldMapping struct {
	GeneratedPrefix string
	SourcePrefix    string
}

// TargetSource identifies the AI Gateway model target and capability that
// produced an ai-proxy-advanced target. It is conversion-only metadata and is
// deliberately never emitted in a Kong declarative configuration.
type TargetSource struct {
	ModelName        string
	ModelTargetIndex int
	Capability       string
}

// Consumer is a Kong Gateway Consumer. Credentials and scoped plugins may be nested.
type Consumer struct {
	ID                 string              `yaml:"id,omitempty"`
	Username           string              `yaml:"username,omitempty"`
	CustomID           string              `yaml:"custom_id,omitempty"`
	Groups             []*Ref              `yaml:"groups,omitempty"`
	KeyAuthCredentials []KeyAuthCredential `yaml:"keyauth_credentials,omitempty"`
	Plugins            []Plugin            `yaml:"plugins,omitempty"`
	Tags               []string            `yaml:"tags,omitempty"`
}

// KeyAuthCredential is a key-auth credential nested under a Consumer.
type KeyAuthCredential struct {
	ID   string   `yaml:"id,omitempty"`
	Key  string   `yaml:"key,omitempty"`
	TTL  *int     `yaml:"ttl,omitempty"`
	Tags []string `yaml:"tags,omitempty"`
}

// ConsumerGroup is a Kong Gateway Consumer Group. Scoped plugins may be nested.
type ConsumerGroup struct {
	ID      string   `yaml:"id,omitempty"`
	Name    string   `yaml:"name"`
	Plugins []Plugin `yaml:"plugins,omitempty"`
	Tags    []string `yaml:"tags,omitempty"`
}

// Vault is a Kong Gateway Vault. Name is the backend type (env, aws, gcp, ...);
// Prefix is the reference prefix used in {vault://<prefix>/...} lookups.
type Vault struct {
	ID          string         `yaml:"id,omitempty"`
	Prefix      string         `yaml:"prefix"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Tags        []string       `yaml:"tags,omitempty"`
}

// CACertificate is a Kong Gateway CA certificate entity: the dataplane-visible
// subset of the AI Gateway CACertificate (control-plane-only fields like
// description and managed_by never cross to Kong). Unlike most decK entities
// it has no name field; the AI Gateway CACertificate.Name is preserved via
// Tags so it can be recovered on revert.
type CACertificate struct {
	ID         string   `yaml:"id,omitempty"`
	Cert       string   `yaml:"cert,omitempty"`
	CertDigest string   `yaml:"cert_digest,omitempty"`
	Tags       []string `yaml:"tags,omitempty"`
}

// Certificate is a Kong Gateway certificate. Kong identifies certificates by
// id only -- the entity has no name -- so SourceName carries the AI Gateway
// certificate name for stable db-less ID derivation and is never serialized.
type Certificate struct {
	ID      string   `yaml:"id,omitempty"`
	Cert    string   `yaml:"cert"`
	Key     string   `yaml:"key,omitempty"`
	CertAlt string   `yaml:"cert_alt,omitempty"`
	KeyAlt  string   `yaml:"key_alt,omitempty"`
	Tags    []string `yaml:"tags,omitempty"`
	SNIs    []SNI    `yaml:"snis,omitempty"`

	SourceName string `yaml:"-"`
}

// SNI is a Kong Gateway SNI, always nested under the certificate it matches
// to -- decK's file format has no top-level snis entity; the relationship is
// expressed purely by nesting.
type SNI struct {
	ID   string   `yaml:"id,omitempty"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags,omitempty"`
}

// AIModel is the Kong Gateway ai-model entity: a named model with an optional
// request-body alias. Plugins scope to it via a Plugin.Model foreign key.
type AIModel struct {
	ID    string   `yaml:"id,omitempty"`
	Name  string   `yaml:"name"`
	Alias string   `yaml:"alias,omitempty"`
	Tags  []string `yaml:"tags,omitempty"`
}
