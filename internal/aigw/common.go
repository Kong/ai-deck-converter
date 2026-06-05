// Package aigw defines the AI Gateway entity-model source types that the
// converter reads. These mirror the schemas in ai-gateway-admin-api.yaml.
// Discriminated unions are decoded with custom UnmarshalYAML that keeps the
// discriminator plus the raw/variant body for later resolution.
package aigw

// Labels is the AI Gateway PublicLabels map (key -> value).
type Labels map[string]string

// ACLs holds allow/deny lists referencing Consumers, Consumer Groups, or
// Authenticated Groups.
type ACLs struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// IsEmpty reports whether the ACL has no allow or deny entries.
func (a ACLs) IsEmpty() bool {
	return len(a.Allow) == 0 && len(a.Deny) == 0
}

// RouteConfig is the AI Gateway route configuration (a subset of Kong Route
// fields). Used to build the Kong Route for a Model/MCP Server/Agent.
type RouteConfig struct {
	Name                    string              `yaml:"name,omitempty"`
	Paths                   []string            `yaml:"paths,omitempty"`
	Hosts                   []string            `yaml:"hosts,omitempty"`
	Methods                 []string            `yaml:"methods,omitempty"`
	Protocols               []string            `yaml:"protocols,omitempty"`
	Headers                 map[string][]string `yaml:"headers,omitempty"`
	StripPath               *bool               `yaml:"strip_path,omitempty"`
	PreserveHost            *bool               `yaml:"preserve_host,omitempty"`
	HTTPSRedirectStatusCode *int                `yaml:"https_redirect_status_code,omitempty"`
	RegexPriority           *int                `yaml:"regex_priority,omitempty"`
	PathHandling            string              `yaml:"path_handling,omitempty"`
	Tags                    []string            `yaml:"tags,omitempty"`
}

// Logging is the AI Gateway logging configuration. Fields are supersets across
// Model (payloads/statistics), MCP Server (+audits), and Agent (+max_payload_size).
type Logging struct {
	Payloads       *bool `yaml:"payloads,omitempty"`
	Statistics     *bool `yaml:"statistics,omitempty"`
	Audits         *bool `yaml:"audits,omitempty"`
	MaxPayloadSize *int  `yaml:"max_payload_size,omitempty"`
}
