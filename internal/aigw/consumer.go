package aigw

// Consumer is an AI Gateway consumer. Credentials are nested here in the input
// envelope (matching the REST sub-resource /consumers/{id}/credentials).
type Consumer struct {
	DisplayName    string       `yaml:"display_name,omitempty"`
	Name           string       `yaml:"name,omitempty"`
	Type           string       `yaml:"type,omitempty"` // api-key | oauth
	CustomID       string       `yaml:"custom_id,omitempty"`
	ConsumerGroups []string     `yaml:"consumer_groups,omitempty"`
	Policies       []string     `yaml:"policies,omitempty"`
	Credentials    []Credential `yaml:"credentials,omitempty"`
	Labels         Labels       `yaml:"labels,omitempty"`
}

// Credential is an AI Gateway consumer credential.
type Credential struct {
	DisplayName string `yaml:"display_name,omitempty"`
	Name        string `yaml:"name,omitempty"`
	Type        string `yaml:"type,omitempty"` // api-key
	TTL         *int   `yaml:"ttl,omitempty"`
	APIKey      string `yaml:"api_key,omitempty"`
}

// ConsumerGroup is an AI Gateway consumer group.
type ConsumerGroup struct {
	DisplayName string   `yaml:"display_name,omitempty"`
	Name        string   `yaml:"name,omitempty"`
	Policies    []string `yaml:"policies,omitempty"`
	Labels      Labels   `yaml:"labels,omitempty"`
}
