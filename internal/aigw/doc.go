package aigw

import "gopkg.in/yaml.v3"

// Document is the input envelope grouping AI Gateway entities by kind. This is
// the file format the converter consumes; each top-level key holds a list of
// the corresponding entity. Credentials are nested under their Consumer.
type Document struct {
	Models         []Model         `yaml:"models,omitempty"`
	ModelProviders []Provider      `yaml:"model_providers,omitempty"`
	MCPServers     []MCPServer     `yaml:"mcp_servers,omitempty"`
	Agents         []Agent         `yaml:"agents,omitempty"`
	Policies       []Policy        `yaml:"policies,omitempty"`
	AuthStrategies []AuthStrategy  `yaml:"auth_strategies,omitempty"`
	Consumers      []Consumer      `yaml:"consumers,omitempty"`
	ConsumerGroups []ConsumerGroup `yaml:"consumer_groups,omitempty"`
	Vaults         []Vault         `yaml:"vaults,omitempty"`
	CACertificates []CACertificate `yaml:"ca_certificates,omitempty"`
	Certificates   []Certificate   `yaml:"certificates,omitempty"`
	SNIs           []SNI           `yaml:"snis,omitempty"`
}

// documentFields mirrors Document without its UnmarshalYAML, so the decoder
// can populate the current keys without recursing.
type documentFields Document

// deprecatedDocument holds the superseded top-level keys that UnmarshalYAML
// folds into their current equivalents. They are accepted silently on input
// and never emitted.
type deprecatedDocument struct {
	// IdentityProviders is the former name of AuthStrategies.
	IdentityProviders []AuthStrategy `yaml:"identity_providers,omitempty"`
}

// UnmarshalYAML decodes a Document, folding deprecated top-level keys into the
// fields that replaced them. Entities given under the current key come first;
// entities under the deprecated key are appended, so a document may set either
// (or, transitionally, both).
func (d *Document) UnmarshalYAML(value *yaml.Node) error {
	var fields documentFields
	if err := value.Decode(&fields); err != nil {
		return err
	}
	var deprecated deprecatedDocument
	if err := value.Decode(&deprecated); err != nil {
		return err
	}
	*d = Document(fields)
	d.AuthStrategies = append(d.AuthStrategies, deprecated.IdentityProviders...)
	return nil
}

// Parse decodes an AI Gateway entity-model document from YAML bytes.
func Parse(data []byte) (*Document, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
