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
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// IsEmpty reports whether the ACL has no allow or deny entries.
func (a ACLs) IsEmpty() bool {
	return len(a.Allow) == 0 && len(a.Deny) == 0
}

// RouteConfig is the AI Gateway route configuration (a subset of Kong Route
// fields). Used to build the Kong Route for a Model/MCP Server/Agent.
type RouteConfig struct {
	Name                    string              `yaml:"name"`
	Paths                   []string            `yaml:"paths"`
	Hosts                   []string            `yaml:"hosts"`
	Methods                 []string            `yaml:"methods"`
	Protocols               []string            `yaml:"protocols"`
	Headers                 map[string][]string `yaml:"headers"`
	StripPath               *bool               `yaml:"strip_path"`
	PreserveHost            *bool               `yaml:"preserve_host"`
	HTTPSRedirectStatusCode *int                `yaml:"https_redirect_status_code"`
	RegexPriority           *int                `yaml:"regex_priority"`
	PathHandling            string              `yaml:"path_handling"`
	Tags                    []string            `yaml:"tags"`
}

// Logging is the AI Gateway logging configuration. Fields are supersets across
// Model (payloads/statistics), MCP Server (+audits), and Agent (+max_payload_size).
type Logging struct {
	Payloads       *bool `yaml:"payloads"`
	Statistics     *bool `yaml:"statistics"`
	Audits         *bool `yaml:"audits"`
	MaxPayloadSize *int  `yaml:"max_payload_size"`
}
