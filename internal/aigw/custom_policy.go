package aigw

// CustomPolicy defines a custom AI Gateway policy. Streaming policies include
// a Lua handler that the data plane loads as a custom plugin.
type CustomPolicy struct {
	ID      string `yaml:"id,omitempty"`
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Schema  string `yaml:"schema"`
	Handler string `yaml:"handler,omitempty"`
}
