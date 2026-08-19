package aigw

// MCPServer is an AI Gateway MCP Server. The discriminator `type` is the mode
// (conversion-only | conversion-listener | listener | passthrough-listener |
// upstream-server), which maps to the ai-mcp-proxy plugin's config.mode.
type MCPServer struct {
	Type        string          `yaml:"type,omitempty"`
	DisplayName string          `yaml:"display_name,omitempty"`
	Name        string          `yaml:"name,omitempty"`
	Enabled     *bool           `yaml:"enabled,omitempty"`
	Config      MCPServerConfig `yaml:"config,omitempty"`
	Tools       []MCPTool       `yaml:"tools,omitempty"`
	Policies    []string        `yaml:"policies,omitempty"`
	Access      MCPAccess       `yaml:"access,omitempty"`
	Labels      Labels          `yaml:"labels,omitempty"`

	// UpstreamURL is the upstream MCP server URL for passthrough-listener mode.
	// Not part of the strict schema (passthrough proxies to the Gateway Service
	// upstream), but accepted here so the converter can build the Kong Service.
	UpstreamURL string `yaml:"upstream_url,omitempty"`
}

// MCPAccess is the access-control configuration for an MCP Server: the ACL
// attribute config, consumer/group ACLs, and the default ACL applied to every
// tool. It also carries the identity-provider reference and OAuth 2.0 Protected
// Resource Metadata used to protect the MCP server (lowered into an
// ai-mcp-oauth2 plugin).
type MCPAccess struct {
	// ACLAttributeType / AccessTokenClaimField map to the ai-mcp-proxy plugin's
	// fields of the same name; the ACLs lower into its default_acl.
	ACLAttributeType      string `yaml:"acl_attribute_type,omitempty"`
	AccessTokenClaimField string `yaml:"access_token_claim_field,omitempty"`
	ACLs                  ACLs   `yaml:"acls,omitempty"`
	DefaultToolACLs       ACLs   `yaml:"default_tool_acls,omitempty"`
	// IdentityProviders references an identity provider (at most one) by name.
	// A key-auth provider becomes a key-auth plugin; an openid-connect provider
	// combined with Metadata becomes an ai-mcp-oauth2 plugin.
	IdentityProviders []string `yaml:"identity_providers,omitempty"`
	// Metadata is the OAuth 2.0 Protected Resource Metadata advertised for this
	// MCP server. When set (with an openid-connect provider), it lowers into an
	// ai-mcp-oauth2 plugin.
	Metadata *MCPProtectedResourceMetadata `yaml:"metadata,omitempty"`
}

// MCPProtectedResourceMetadata is the OAuth 2.0 Protected Resource Metadata
// (RFC 9728) advertised for an MCP server, allowing clients to discover the
// authorization servers that protect it. It maps to the ai-mcp-oauth2 plugin's
// resource / authorization_servers / scopes_supported / metadata_endpoint.
type MCPProtectedResourceMetadata struct {
	DiscoveryEndpoint    string   `yaml:"discovery_endpoint,omitempty"`
	Endpoint             string   `yaml:"endpoint,omitempty"`
	AuthorizationServers []string `yaml:"authorization_servers,omitempty"`
	Resource             string   `yaml:"resource,omitempty"`
	ScopesSupported      []string `yaml:"scopes_supported,omitempty"`
}

// MCPServerConfig holds routing, logging, proxy, and server configuration.
// Access control lives on the MCPServer itself (see MCPAccess), not here.
type MCPServerConfig struct {
	Route              RouteConfig    `yaml:"route,omitempty"`
	Logging            *Logging       `yaml:"logging,omitempty"`
	MaxRequestBodySize *int           `yaml:"max_request_body_size,omitempty"`
	Server             map[string]any `yaml:"server,omitempty"`
	// Proxy lowers to the ai-mcp-proxy plugin's proxy_config (only honored by
	// the plugin in passthrough-listener mode).
	Proxy *ProxyConfig `yaml:"proxy,omitempty"`
	// Upstream lowers to the ai-mcp-proxy plugin's auth record (upstream
	// authentication, e.g. AWS SigV4).
	Upstream *UpstreamConfig `yaml:"upstream,omitempty"`
	// ToolsCacheTTLSeconds maps to the ai-mcp-proxy plugin's
	// tools_cache_ttl_seconds (required by the plugin in upstream-server mode).
	ToolsCacheTTLSeconds *int `yaml:"tools_cache_ttl_seconds,omitempty"`
	// Sources lists, for a `listener` MCP server, the names of the source MCP
	// servers (conversion-only toolsets / upstream-server third-party servers)
	// whose tools the listener exposes. The converter attaches the listener's
	// private server.tag to each referenced source plugin's tags so the DP
	// exposes exactly those sources' tools on the listener.
	Sources []string `yaml:"sources,omitempty"`
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
