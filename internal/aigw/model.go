package aigw

import "gopkg.in/yaml.v3"

// Model is an AI Gateway model. The discriminator `type` is either "model"
// (synchronous generative APIs) or "api" (files/batches); both share the same
// shape and differ only in their allowed capabilities, so one struct covers both.
type Model struct {
	Ref          string        `yaml:"ref,omitempty"`
	ID           string        `yaml:"id,omitempty"`
	Type         string        `yaml:"type,omitempty"`
	DisplayName  string        `yaml:"display_name,omitempty"`
	Name         string        `yaml:"name,omitempty"`
	Enabled      *bool         `yaml:"enabled,omitempty"`
	Capabilities []string      `yaml:"capabilities,omitempty"`
	Formats      []Format      `yaml:"formats,omitempty"`
	TargetModels []TargetModel `yaml:"targets,omitempty"`
	Policies     []string      `yaml:"policies,omitempty"`
	Access       ModelAccess   `yaml:"access,omitempty"`
	Config       ModelConfig   `yaml:"config,omitempty"`
	Labels       Labels        `yaml:"labels,omitempty"`
}

// ModelAccess is the access-control configuration for a Model: identity
// providers gating the model's route, plus consumer/group ACLs.
type ModelAccess struct {
	IdentityProviders []string `yaml:"identity_providers,omitempty"`
	ACLs              ACLs     `yaml:"acls,omitempty"`
}

// Format is a request/response format supported by a model.
type Format struct {
	Type string `yaml:"type,omitempty"` // anthropic|bedrock|cohere|gemini|huggingface|openai
}

// ModelConfig holds routing, logging, and load-balancing configuration.
type ModelConfig struct {
	Route              RouteConfig   `yaml:"route,omitempty"`
	Logging            *Logging      `yaml:"logging,omitempty"`
	ResponseStreaming  string        `yaml:"response_streaming,omitempty"`
	MaxRequestBodySize *int          `yaml:"max_request_body_size,omitempty"`
	Model              ModelSelector `yaml:"model,omitempty"`
	Proxy              *ProxyConfig  `yaml:"proxy,omitempty"`
	Balancer           *Balancer     `yaml:"balancer,omitempty"`
}

// ModelSelector configures how the request model name is interpreted.
type ModelSelector struct {
	Alias      string `yaml:"alias,omitempty"`
	NameHeader *bool  `yaml:"name_header,omitempty"`
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

// MarshalYAML re-merges the algorithm discriminator with the tuning fields
// (inverse of UnmarshalYAML, used by the reverse converter).
func (b Balancer) MarshalYAML() (any, error) {
	m := make(map[string]any, len(b.Fields)+1)
	for k, v := range b.Fields {
		m[k] = v
	}
	if b.Algorithm != "" {
		m["algorithm"] = b.Algorithm
	}
	return m, nil
}
