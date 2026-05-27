package aigw

// Consumer is an AI Gateway consumer. Credentials are nested here in the input
// envelope (matching the REST sub-resource /consumers/{id}/credentials).
type Consumer struct {
	DisplayName    string       `yaml:"display_name"`
	Name           string       `yaml:"name"`
	Type           string       `yaml:"type"` // api-key | oauth
	CustomID       string       `yaml:"custom_id"`
	ConsumerGroups []string     `yaml:"consumer_groups"`
	Policies       []string     `yaml:"policies"`
	Credentials    []Credential `yaml:"credentials"`
	Labels         Labels       `yaml:"labels"`
}

// Credential is an AI Gateway consumer credential.
type Credential struct {
	DisplayName string `yaml:"display_name"`
	Name        string `yaml:"name"`
	Type        string `yaml:"type"` // api-key
	TTL         *int   `yaml:"ttl"`
	APIKey      string `yaml:"api_key"`
}

// ConsumerGroup is an AI Gateway consumer group.
type ConsumerGroup struct {
	DisplayName string   `yaml:"display_name"`
	Name        string   `yaml:"name"`
	Policies    []string `yaml:"policies"`
	Labels      Labels   `yaml:"labels"`
}
