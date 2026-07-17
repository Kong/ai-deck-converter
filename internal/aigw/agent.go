package aigw

// Agent is an AI Gateway agent. type is "a2a" (gets an ai-a2a-proxy plugin) or
// "http" (plain proxy Service+Route). Both share the same config shape.
type Agent struct {
	Ref         string       `yaml:"ref,omitempty"`
	Type        string       `yaml:"type,omitempty"`
	DisplayName string       `yaml:"display_name,omitempty"`
	Name        string       `yaml:"name,omitempty"`
	Enabled     *bool        `yaml:"enabled,omitempty"`
	Policies    []string     `yaml:"policies,omitempty"`
	Access      AccessConfig `yaml:"access,omitempty"`
	Config      AgentConfig  `yaml:"config,omitempty"`
	Labels      Labels       `yaml:"labels,omitempty"`
}

// AccessConfig holds the access-related configuration.
type AccessConfig struct {
	ACLs ACLs `yaml:"acls,omitempty"`
}

// AgentConfig holds the upstream URL, route, and logging configuration.
type AgentConfig struct {
	URL                string      `yaml:"url,omitempty"`
	Route              RouteConfig `yaml:"route,omitempty"`
	MaxRequestBodySize *int        `yaml:"max_request_body_size,omitempty"`
	Logging            *Logging    `yaml:"logging,omitempty"`
}
