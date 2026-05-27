package aigw

// Agent is an AI Gateway agent. type is "a2a" (gets an ai-a2a-proxy plugin) or
// "http" (plain proxy Service+Route). Both share the same config shape.
type Agent struct {
	Type        string      `yaml:"type"`
	DisplayName string      `yaml:"display_name"`
	Name        string      `yaml:"name"`
	Enabled     *bool       `yaml:"enabled"`
	Policies    []string    `yaml:"policies"`
	ACLs        ACLs        `yaml:"acls"`
	Config      AgentConfig `yaml:"config"`
	Labels      Labels      `yaml:"labels"`
}

// AgentConfig holds the upstream URL, route, and logging configuration.
type AgentConfig struct {
	URL                string      `yaml:"url"`
	Route              RouteConfig `yaml:"route"`
	MaxRequestBodySize *int        `yaml:"max_request_body_size"`
	Logging            *Logging    `yaml:"logging"`
}
