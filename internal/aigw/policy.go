package aigw

// Policy is an AI Gateway policy. Its `type` equals a Kong plugin name and its
// config is passed through. A global policy becomes a global Kong plugin;
// otherwise it is instantiated as a scoped plugin on each referencing entity.
type Policy struct {
	ID          string         `yaml:"id,omitempty"`
	Type        string         `yaml:"type,omitempty"`
	DisplayName string         `yaml:"display_name,omitempty"`
	Name        string         `yaml:"name,omitempty"`
	Enabled     *bool          `yaml:"enabled,omitempty"`
	Global      *bool          `yaml:"global,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Labels      Labels         `yaml:"labels,omitempty"`
	Datastore   *Datastore     `yaml:"datastore,omitempty"`
}

// Datastore is a shared connection resolved out-of-band (Konnect's datastore
// API) and attached here fully resolved. Type is one of "redis-ce", "redis-ee",
// "vectordb" (a pgvector connection, despite the name); Config carries that
// backend's connection fields verbatim, already shaped like the Kong plugin
// fields they will replace (see convert/policy.go's datastoreVectorDBGroup).
type Datastore struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}
