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
	Datastores  []DatastoreRef `yaml:"datastores,omitempty"`
}

// DatastoreRef references a top-level datastore by name. It is always a
// mapping: `datastore: [{name: my-redis}]`.
//
// Path is reserved and not honored yet.
type DatastoreRef struct {
	Name string  `yaml:"name"`
	Path *string `yaml:"path,omitempty"`
}
