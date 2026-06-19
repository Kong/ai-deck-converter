package aigw

// MCPServer is an AI Gateway MCP Server. The discriminator `type` is the mode
// (conversion-only | conversion-listener | listener | passthrough-listener |
// upstream-server), which maps to the ai-mcp-proxy plugin's config.mode.
type MCPServer struct {
	Type            string          `yaml:"type,omitempty"`
	DisplayName     string          `yaml:"display_name,omitempty"`
	Name            string          `yaml:"name,omitempty"`
	Enabled         *bool           `yaml:"enabled,omitempty"`
	Config          MCPServerConfig `yaml:"config,omitempty"`
	Tools           []MCPTool       `yaml:"tools,omitempty"`
	Policies        []string        `yaml:"policies,omitempty"`
	ACLs            ACLs            `yaml:"acls,omitempty"`
	DefaultToolACLs ACLs            `yaml:"default_tool_acls,omitempty"`
	Labels          Labels          `yaml:"labels,omitempty"`

	// UpstreamURL is the upstream MCP server URL for passthrough-listener mode.
	// Not part of the strict schema (passthrough proxies to the Gateway Service
	// upstream), but accepted here so the converter can build the Kong Service.
	UpstreamURL string `yaml:"upstream_url,omitempty"`
}

// MCPServerConfig holds routing, logging, auth, proxy, and server configuration.
type MCPServerConfig struct {
	Route              RouteConfig    `yaml:"route,omitempty"`
	Logging            *Logging       `yaml:"logging,omitempty"`
	MaxRequestBodySize *int           `yaml:"max_request_body_size,omitempty"`
	Server             map[string]any `yaml:"server,omitempty"`
	// Auth carries the server-level ACL configuration. It maps to the
	// ai-mcp-proxy plugin's acl_attribute_type / access_token_claim_field /
	// default_acl fields.
	Auth *MCPAuth `yaml:"auth,omitempty"`
	// Proxy maps directly to the ai-mcp-proxy plugin's proxy_config (only
	// honored by the plugin in passthrough-listener mode).
	Proxy map[string]any `yaml:"proxy,omitempty"`
	// ToolsCacheTTLSeconds maps to the ai-mcp-proxy plugin's
	// tools_cache_ttl_seconds (required by the plugin in upstream-server mode).
	ToolsCacheTTLSeconds *int `yaml:"tools_cache_ttl_seconds,omitempty"`
}

// MCPAuth is the server-level ACL configuration of an MCP Server. It maps to the
// ai-mcp-proxy plugin's acl_attribute_type, access_token_claim_field, and
// default_acl fields.
type MCPAuth struct {
	ACLAttributeType      string `yaml:"acl_attribute_type,omitempty"`
	AccessTokenClaimField string `yaml:"access_token_claim_field,omitempty"`
	ACLs                  ACLs   `yaml:"acls,omitempty"`
	DefaultToolACLs       ACLs   `yaml:"default_tool_acls,omitempty"`
}

// MCPTool is a single MCP tool definition. Fields mirror the ai-mcp-proxy
// config.tools[] shape; ACLs are handled separately (consumer/group references).
type MCPTool struct {
	Name        string           `yaml:"name,omitempty"`
	Description string           `yaml:"description,omitempty"`
	Method      string           `yaml:"method,omitempty"`
	Path        string           `yaml:"path,omitempty"`
	Scheme      string           `yaml:"scheme,omitempty"`
	Host        string           `yaml:"host,omitempty"`
	Headers     map[string]any   `yaml:"headers,omitempty"`
	Query       map[string]any   `yaml:"query,omitempty"`
	RequestBody map[string]any   `yaml:"request_body,omitempty"`
	Responses   map[string]any   `yaml:"responses,omitempty"`
	Parameters  []map[string]any `yaml:"parameters,omitempty"`
	Annotations map[string]any   `yaml:"annotations,omitempty"`
	ACLs        *ACLs            `yaml:"acls,omitempty"`
	// InputSchema / OutputSchema are only honored by the plugin in
	// upstream-server mode; they override the upstream server's schema for the
	// tool of the same name.
	InputSchema  map[string]any `yaml:"input_schema,omitempty"`
	OutputSchema map[string]any `yaml:"output_schema,omitempty"`
}
