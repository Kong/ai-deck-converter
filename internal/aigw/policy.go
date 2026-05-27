package aigw

// Policy is an AI Gateway policy. Its `type` equals a Kong plugin name and its
// config is passed through. A global policy becomes a global Kong plugin;
// otherwise it is instantiated as a scoped plugin on each referencing entity.
type Policy struct {
	Type        string         `yaml:"type"`
	DisplayName string         `yaml:"display_name"`
	Name        string         `yaml:"name"`
	Enabled     *bool          `yaml:"enabled"`
	Global      *bool          `yaml:"global"`
	Config      map[string]any `yaml:"config"`
	Labels      Labels         `yaml:"labels"`
}
