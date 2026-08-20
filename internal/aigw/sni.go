package aigw

// SNI is an AI Gateway SNI: a hostname used for TLS Server Name Indication
// matching, mapped to the Certificate (by name) that should be presented for
// it. name is the stable human-readable identifier; it is immutable in the AI
// Gateway API and distinct from hostname.
type SNI struct {
	ID          string `yaml:"id,omitempty"`
	Name        string `yaml:"name,omitempty"`
	DisplayName string `yaml:"display_name,omitempty"`
	Hostname    string `yaml:"hostname,omitempty"`
	Certificate string `yaml:"certificate,omitempty"`
	Labels      Labels `yaml:"labels,omitempty"`
}
