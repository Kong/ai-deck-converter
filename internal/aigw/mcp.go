package aigw

// MCPServer is an AI Gateway MCP Server. The discriminator `type` is the mode
// (conversion-only | conversion-listener | listener | passthrough-listener |
// upstream-server), which maps to the ai-mcp-proxy plugin's config.mode.
type MCPServer struct {
	Ref         string          `yaml:"ref,omitempty"`
	Type        string          `yaml:"type,omitempty"`
	DisplayName string          `yaml:"display_name,omitempty"`
	Name        string          `yaml:"name,omitempty"`
	Enabled     *bool           `yaml:"enabled,omitempty"`
	Config      MCPServerConfig `yaml:"config,omitempty"`
	Tools       []MCPTool       `yaml:"tools,omitempty"`
	Policies    []string        `yaml:"policies,omitempty"`
	Access      MCPAccess       `yaml:"access,omitempty"`
	Labels      Labels          `yaml:"labels,omitempty"`

	// UpstreamURL is a legacy, input-only alias for the upstream MCP server URL.
	// The AI Gateway schema carries the upstream URL as config.url (see
	// MCPServerConfig.URL); this top-level field is still accepted on input for
	// backward compatibility with older hand-written configs but is never emitted.
	UpstreamURL string `yaml:"upstream_url,omitempty"`
}

// MCPAccess is the access-control configuration for an MCP Server: consumer/
// group ACLs plus the default ACL applied to every tool.
type MCPAccess struct {
	ACLs            ACLs `yaml:"acls,omitempty"`
	DefaultToolACLs ACLs `yaml:"default_tool_acls,omitempty"`
}

// MCPServerConfig holds routing, logging, access, proxy, and server configuration.
type MCPServerConfig struct {
	Route              RouteConfig    `yaml:"route,omitempty"`
	Logging            *Logging       `yaml:"logging,omitempty"`
	MaxRequestBodySize *int           `yaml:"max_request_body_size,omitempty"`
	Server             map[string]any `yaml:"server,omitempty"`
	// URL is the upstream MCP server URL (${scheme}://${host}:${port}/${path}),
	// required by the AI Gateway schema for every mode except listener. It lowers
	// to the Kong Gateway Service URL.
	URL string `yaml:"url,omitempty"`
	// Access carries the attribute-based ACL configuration. It maps to the
	// ai-mcp-proxy plugin's acl_attribute_type / access_token_claim_field /
	// default_acl fields.
	Access *MCPConfigAccess `yaml:"access,omitempty"`
	// Proxy lowers to the ai-mcp-proxy plugin's proxy_config (only honored by
	// the plugin in passthrough-listener mode).
	Proxy *ProxyConfig `yaml:"proxy,omitempty"`
	// ToolsCacheTTLSeconds maps to the ai-mcp-proxy plugin's
	// tools_cache_ttl_seconds (required by the plugin in upstream-server mode).
	ToolsCacheTTLSeconds *int `yaml:"tools_cache_ttl_seconds,omitempty"`
}

// MCPConfigAccess is the attribute-based ACL configuration of an MCP Server
// (config.access). It maps to the ai-mcp-proxy plugin's acl_attribute_type,
// access_token_claim_field, and default_acl fields.
type MCPConfigAccess struct {
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
	Access      AccessConfig     `yaml:"access,omitempty"`
	// InputSchema / OutputSchema are only honored by the plugin in
	// upstream-server mode; they override the upstream server's schema for the
	// tool of the same name.
	InputSchema  map[string]any `yaml:"input_schema,omitempty"`
	OutputSchema map[string]any `yaml:"output_schema,omitempty"`
}
