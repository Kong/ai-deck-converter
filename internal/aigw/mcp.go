package aigw

// MCPServer is an AI Gateway MCP Server. The discriminator `type` is the mode
// (conversion-only | conversion-listener | listener | passthrough-listener),
// which maps to the ai-mcp-proxy plugin's config.mode.
type MCPServer struct {
	Type            string          `yaml:"type"`
	DisplayName     string          `yaml:"display_name"`
	Name            string          `yaml:"name"`
	Enabled         *bool           `yaml:"enabled"`
	Config          MCPServerConfig `yaml:"config"`
	Tools           []MCPTool       `yaml:"tools"`
	Policies        []string        `yaml:"policies"`
	ACLs            ACLs            `yaml:"acls"`
	DefaultToolACLs ACLs            `yaml:"default_tool_acls"`
	Labels          Labels          `yaml:"labels"`

	// UpstreamURL is the upstream MCP server URL for passthrough-listener mode.
	// Not part of the strict schema (passthrough proxies to the Gateway Service
	// upstream), but accepted here so the converter can build the Kong Service.
	UpstreamURL string `yaml:"upstream_url"`
}

// MCPServerConfig holds routing, logging, and server configuration.
type MCPServerConfig struct {
	Route              RouteConfig    `yaml:"route"`
	Logging            *Logging       `yaml:"logging"`
	MaxRequestBodySize *int           `yaml:"max_request_body_size"`
	Server             map[string]any `yaml:"server"`
}

// MCPTool is a single MCP tool definition. Fields mirror the ai-mcp-proxy
// config.tools[] shape; ACLs are handled separately (consumer/group references).
type MCPTool struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Method      string           `yaml:"method"`
	Path        string           `yaml:"path"`
	Scheme      string           `yaml:"scheme"`
	Host        string           `yaml:"host"`
	Headers     map[string]any   `yaml:"headers"`
	Query       map[string]any   `yaml:"query"`
	RequestBody map[string]any   `yaml:"request_body"`
	Responses   map[string]any   `yaml:"responses"`
	Parameters  []map[string]any `yaml:"parameters"`
	Annotations map[string]any   `yaml:"annotations"`
	ACLs        *ACLs            `yaml:"acls"`
}
