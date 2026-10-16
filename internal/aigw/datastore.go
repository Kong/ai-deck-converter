package aigw

// Datastore is a shared connection that a Policy, a Model, or an MCP server
// references by name in its `datastores` field.
// Type is one of "redis-ce", "redis-ee", "vectordb", or "token-vault".
// The "vectordb" type is a pgvector connection, despite the name.
// Policies and Models use the redis and vectordb types.
// MCP servers use the "token-vault" type.
// The named constants are in internal/aimap/datastore.go.
// Config carries the connection fields of that type.
// The redis and vectordb fields already have the shape of plugin fields.
// For token-vault, "directory" goes to the plugin config.
// The other token-vault fields go to its storage block.
type Datastore struct {
	Type   string         `yaml:"type,omitempty"`
	Name   string         `yaml:"name,omitempty"`
	Config map[string]any `yaml:"config,omitempty"`
}
