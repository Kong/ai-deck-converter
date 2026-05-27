package aigw

import "gopkg.in/yaml.v3"

// Model is an AI Gateway model. The discriminator `type` is either "model"
// (synchronous generative APIs) or "api" (files/batches); both share the same
// shape and differ only in their allowed capabilities, so one struct covers both.
type Model struct {
	Type         string        `yaml:"type"`
	DisplayName  string        `yaml:"display_name"`
	Name         string        `yaml:"name"`
	Enabled      *bool         `yaml:"enabled"`
	Capabilities []string      `yaml:"capabilities"`
	Formats      []Format      `yaml:"formats"`
	TargetModels []TargetModel `yaml:"target_models"`
	Policies     []string      `yaml:"policies"`
	ACLs         ACLs          `yaml:"acls"`
	Config       ModelConfig   `yaml:"config"`
	Labels       Labels        `yaml:"labels"`
}

// Format is a request/response format supported by a model.
type Format struct {
	Type string `yaml:"type"` // anthropic|bedrock|cohere|gemini|huggingface|openai
}

// ModelConfig holds routing, logging, and load-balancing configuration.
type ModelConfig struct {
	Route              RouteConfig   `yaml:"route"`
	Logging            *Logging      `yaml:"logging"`
	ResponseStreaming  string        `yaml:"response_streaming"`
	MaxRequestBodySize *int          `yaml:"max_request_body_size"`
	Model              ModelSelector `yaml:"model"`
	Balancer           *Balancer     `yaml:"balancer"`
}

// ModelSelector configures how the request model name is interpreted.
type ModelSelector struct {
	Alias      string `yaml:"alias"`
	NameHeader *bool  `yaml:"name_header"`
}

// Balancer is the model load-balancer config, discriminated by `algorithm`.
// Algorithm-specific tuning fields are retained in Fields for passthrough into
// the ai-proxy-advanced config.balancer block.
type Balancer struct {
	Algorithm string
	Fields    map[string]any
}

// UnmarshalYAML splits the balancer into its algorithm discriminator and the
// remaining tuning fields.
func (b *Balancer) UnmarshalYAML(node *yaml.Node) error {
	var m map[string]any
	if err := node.Decode(&m); err != nil {
		return err
	}
	if algo, ok := m["algorithm"].(string); ok {
		b.Algorithm = algo
		delete(m, "algorithm")
	}
	b.Fields = m
	return nil
}
