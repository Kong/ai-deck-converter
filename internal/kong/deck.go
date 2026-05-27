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
	AIModels       []AIModel       `yaml:"ai-models,omitempty"`
}

// Ref is a name-based foreign-key reference, rendered as `{name: <x>}`.
type Ref struct {
	Name string `yaml:"name"`
}

// NewRef returns a pointer to a Ref with the given name.
func NewRef(name string) *Ref { return &Ref{Name: name} }

// NewDocument returns an empty document with the format version set.
func NewDocument() *Document {
	return &Document{FormatVersion: FormatVersion}
}

// Service is a Kong Gateway Service. Routes and Plugins may be nested.
type Service struct {
	Name     string   `yaml:"name"`
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
}

// Route is a Kong Gateway Route. Plugins may be nested.
type Route struct {
	Name         string              `yaml:"name"`
	Paths        []string            `yaml:"paths,omitempty"`
	Hosts        []string            `yaml:"hosts,omitempty"`
	Methods      []string            `yaml:"methods,omitempty"`
	Protocols    []string            `yaml:"protocols,omitempty"`
	Headers      map[string][]string `yaml:"headers,omitempty"`
	StripPath    *bool               `yaml:"strip_path,omitempty"`
	PreserveHost *bool               `yaml:"preserve_host,omitempty"`
	Plugins      []Plugin            `yaml:"plugins,omitempty"`
	Tags         []string            `yaml:"tags,omitempty"`
}

// Plugin is a Kong Gateway Plugin. When emitted at the top level, the foreign-key
// reference fields name the entity it is scoped to (rendered as `{name: <x>}`).
// When nested under an entity, those fields are left nil.
type Plugin struct {
	Name          string         `yaml:"name"`
	Enabled       *bool          `yaml:"enabled,omitempty"`
	Config        map[string]any `yaml:"config,omitempty"`
	Service       *Ref           `yaml:"service,omitempty"`
	Route         *Ref           `yaml:"route,omitempty"`
	Consumer      *Ref           `yaml:"consumer,omitempty"`
	ConsumerGroup *Ref           `yaml:"consumer_group,omitempty"`
	Model         *Ref           `yaml:"model,omitempty"`
	Tags          []string       `yaml:"tags,omitempty"`
}

// Consumer is a Kong Gateway Consumer. Credentials and scoped plugins may be nested.
type Consumer struct {
	Username           string              `yaml:"username,omitempty"`
	CustomID           string              `yaml:"custom_id,omitempty"`
	Groups             []string            `yaml:"groups,omitempty"`
	KeyAuthCredentials []KeyAuthCredential `yaml:"keyauth_credentials,omitempty"`
	Plugins            []Plugin            `yaml:"plugins,omitempty"`
	Tags               []string            `yaml:"tags,omitempty"`
}

// KeyAuthCredential is a key-auth credential nested under a Consumer.
type KeyAuthCredential struct {
	Key  string   `yaml:"key,omitempty"`
	TTL  *int     `yaml:"ttl,omitempty"`
	Tags []string `yaml:"tags,omitempty"`
}

// ConsumerGroup is a Kong Gateway Consumer Group. Scoped plugins may be nested.
type ConsumerGroup struct {
	Name    string   `yaml:"name"`
	Plugins []Plugin `yaml:"plugins,omitempty"`
	Tags    []string `yaml:"tags,omitempty"`
}

// Vault is a Kong Gateway Vault. Name is the backend type (env, aws, gcp, ...);
// Prefix is the reference prefix used in {vault://<prefix>/...} lookups.
type Vault struct {
	Prefix      string         `yaml:"prefix"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Tags        []string       `yaml:"tags,omitempty"`
}

// AIModel is the Kong Gateway ai-model entity: a named model with an optional
// request-body alias. Plugins scope to it via a Plugin.Model foreign key.
type AIModel struct {
	Name  string `yaml:"name"`
	Alias string `yaml:"alias,omitempty"`
}
