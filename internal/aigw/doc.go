package aigw

import "gopkg.in/yaml.v3"

// Document is the input envelope grouping AI Gateway entities by kind. This is
// the file format the converter consumes; each top-level key holds a list of
// the corresponding entity. Credentials are nested under their Consumer.
type Document struct {
	Models         []Model         `yaml:"models"`
	Providers      []Provider      `yaml:"providers"`
	MCPServers     []MCPServer     `yaml:"mcp_servers"`
	Agents         []Agent         `yaml:"agents"`
	Policies       []Policy        `yaml:"policies"`
	Consumers      []Consumer      `yaml:"consumers"`
	ConsumerGroups []ConsumerGroup `yaml:"consumer_groups"`
	Vaults         []Vault         `yaml:"vaults"`
}

// Parse decodes an AI Gateway entity-model document from YAML bytes.
func Parse(data []byte) (*Document, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
