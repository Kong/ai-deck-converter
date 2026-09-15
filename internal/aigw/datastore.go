package aigw

// Datastore is a shared connection resolved out-of-band (Konnect's datastore
// API) and referenced by name from a Policy's `datastore` field. Type is one
// of "redis-ce", "redis-ee", "vectordb" (a pgvector connection, despite the
// name) — see internal/aimap/datastore.go, where those values are validated
// and interpreted, for the named constants. Config carries that backend's
// connection fields verbatim, already shaped like the Kong plugin fields they
// will replace.
type Datastore struct {
	Type   string         `yaml:"type,omitempty"`
	Name   string         `yaml:"name,omitempty"`
	Config map[string]any `yaml:"config,omitempty"`
}
