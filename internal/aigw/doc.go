package aigw

import "gopkg.in/yaml.v3"

// Document is the input envelope grouping AI Gateway entities by kind. This is
// the file format the converter consumes; each top-level key holds a list of
// the corresponding entity. Credentials are nested under their Consumer.
type Document struct {
	Models            []Model            `yaml:"models,omitempty"`
	Providers         []Provider         `yaml:"providers,omitempty"`
	MCPServers        []MCPServer        `yaml:"mcp_servers,omitempty"`
	Agents            []Agent            `yaml:"agents,omitempty"`
	Policies          []Policy           `yaml:"policies,omitempty"`
	IdentityProviders []IdentityProvider `yaml:"identity_providers,omitempty"`
	Consumers         []Consumer         `yaml:"consumers,omitempty"`
	ConsumerGroups    []ConsumerGroup    `yaml:"consumer_groups,omitempty"`
	Vaults            []Vault            `yaml:"vaults,omitempty"`
}

// Parse decodes an AI Gateway entity-model document from YAML bytes.
func Parse(data []byte) (*Document, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
