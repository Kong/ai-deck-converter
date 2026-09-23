package aigw

import "gopkg.in/yaml.v3"

// MCPServer is an AI Gateway MCP Server. The discriminator `type` is the mode
// (conversion-only | conversion-listener | listener | passthrough-listener |
// upstream-server), which maps to the ai-mcp-proxy plugin's config.mode.
type MCPServer struct {
	// ID is the Konnect entity UUID
	ID          string          `yaml:"id,omitempty"`
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
	// TokenVault resolves the upstream credential per request via Kong's Token
	// Vault instead of a static credential. It lowers into the ai-mcp-proxy
	// plugin's auth record (auth.provider: token_vault), so it is mutually
	// exclusive with config.upstream.auth.
	TokenVault *TokenVaultConfig `yaml:"token_vault,omitempty"`
}

// TokenVaultConfig mirrors the AIGatewayTokenVault schema. It resolves an
// upstream credential per request via Kong's Token Vault: exchanged
// credentials are cached per node and, when Redis is configured, shared
// across the cluster (encrypted). Lowered into the ai-mcp-proxy plugin's
// auth.token_vault record.
type TokenVaultConfig struct {
	// Directory is the directory name segment of the vault token endpoint.
	Directory string `yaml:"directory,omitempty"`
	// Provider is the upstream credential provider registered in the Token
	// Vault directory.
	Provider string `yaml:"provider,omitempty"`
	// Redis shares exchanged credentials across the cluster.
	Redis *RedisCloudConfig `yaml:"redis,omitempty"`
	// EncryptionSecrets encrypt exchanged credentials before they are cached
	// in Redis. Required when Redis is configured. Referenceable
	// ({vault://...} references pass through verbatim).
	EncryptionSecrets []string `yaml:"encryption_secrets,omitempty"`
}

// RedisCloudConfig mirrors the AIGatewayRedisCloudConfiguration schema: the
// connection settings for a Redis instance backing the Token Vault's
// cross-node credential cache. It is translated into the flat field surface
// of the Kong redis-ee config schema (aimap.TokenVaultRedisToPlugin) — the
// nested keepalive/sentinel/cluster/cloud_authentication blocks flatten to
// prefixed keys there.
type RedisCloudConfig struct {
	Host                string                    `yaml:"host,omitempty"`
	Port                *int                      `yaml:"port,omitempty"`
	Database            *int                      `yaml:"database,omitempty"`
	Username            string                    `yaml:"username,omitempty"`
	Password            string                    `yaml:"password,omitempty"`
	SSL                 *bool                     `yaml:"ssl,omitempty"`
	SSLVerify           *bool                     `yaml:"ssl_verify,omitempty"`
	ServerName          string                    `yaml:"server_name,omitempty"`
	ConnectionIsProxied *bool                     `yaml:"connection_is_proxied,omitempty"`
	ConnectTimeout      *int                      `yaml:"connect_timeout,omitempty"`
	ReadTimeout         *int                      `yaml:"read_timeout,omitempty"`
	SendTimeout         *int                      `yaml:"send_timeout,omitempty"`
	Keepalive           *RedisKeepaliveConfig     `yaml:"keepalive,omitempty"`
	Sentinel            *RedisSentinelConfig      `yaml:"sentinel,omitempty"`
	Cluster             *RedisClusterConfig       `yaml:"cluster,omitempty"`
	CloudAuthentication *RedisCloudAuthentication `yaml:"cloud_authentication,omitempty"`
}

// RedisKeepaliveConfig is the connection-pool tuning of a Redis connection.
type RedisKeepaliveConfig struct {
	PoolSize *int `yaml:"pool_size,omitempty"`
	Backlog  *int `yaml:"backlog,omitempty"`
}

// RedisSentinelConfig configures Redis Sentinel discovery.
type RedisSentinelConfig struct {
	Master   string      `yaml:"master,omitempty"`
	Role     string      `yaml:"role,omitempty"`
	Username string      `yaml:"username,omitempty"`
	Password string      `yaml:"password,omitempty"`
	Nodes    []RedisNode `yaml:"nodes,omitempty"`
}

// RedisClusterConfig configures Redis Cluster discovery.
type RedisClusterConfig struct {
	MaxRedirections *int        `yaml:"max_redirections,omitempty"`
	Nodes           []RedisNode `yaml:"nodes,omitempty"`
}

// RedisNode is one address in a sentinel.nodes / cluster.nodes list. Sentinel
// nodes address the host by `host`, cluster nodes by `ip`; the plugin schema
// uses the same split (sentinel_nodes[].host vs cluster_nodes[].ip), while the
// API model reuses one shape for both.
type RedisNode struct {
	Host string `yaml:"host,omitempty"`
	IP   string `yaml:"ip,omitempty"`
	Port *int   `yaml:"port,omitempty"`
}

// RedisCloudAuthentication carries the cloud-provider credentials for a
// managed Redis instance (AWS ElastiCache, Azure Cache, GCP Memorystore).
// Type is the discriminator (aws | azure | gcp); the remaining fields are the
// selected variant's. Lowered to the plugin's auth_provider + prefixed-key
// shape (aimap.TokenVaultRedisToPlugin).
type RedisCloudAuthentication struct {
	Type               string `yaml:"type,omitempty"`
	AccessKeyID        string `yaml:"access_key_id,omitempty"`
	SecretAccessKey    string `yaml:"secret_access_key,omitempty"`
	CacheName          string `yaml:"cache_name,omitempty"`
	IsServerless       *bool  `yaml:"is_serverless,omitempty"`
	Region             string `yaml:"region,omitempty"`
	AssumeRoleARN      string `yaml:"assume_role_arn,omitempty"`
	RoleSessionName    string `yaml:"role_session_name,omitempty"`
	ClientID           string `yaml:"client_id,omitempty"`
	ClientSecret       string `yaml:"client_secret,omitempty"`
	TenantID           string `yaml:"tenant_id,omitempty"`
	ServiceAccountJSON string `yaml:"service_account_json,omitempty"`
}

// MCPAccess is the access-control configuration for an MCP Server: the ACL
// attribute config, consumer/group ACLs, and the default ACL applied to every
// tool. It also carries the auth-strategy reference and OAuth 2.0 Protected
// Resource Metadata used to protect the MCP server (lowered into an
// ai-mcp-oauth2 plugin).
type MCPAccess struct {
	// ACLAttributeType / AccessTokenClaimField map to the ai-mcp-proxy plugin's
	// fields of the same name; the ACLs lower into its default_acl.
	ACLAttributeType      string `yaml:"acl_attribute_type,omitempty"`
	AccessTokenClaimField string `yaml:"access_token_claim_field,omitempty"`
	ACLs                  ACLs   `yaml:"acls,omitempty"`
	DefaultToolACLs       ACLs   `yaml:"default_tool_acls,omitempty"`
	// AuthStrategies references an auth strategy (at most one) by name.
	// A key-auth strategy becomes a key-auth plugin; an openid-connect strategy
	// combined with Metadata becomes an ai-mcp-oauth2 plugin.
	AuthStrategies []string `yaml:"auth_strategies,omitempty"`
	// Metadata is the OAuth 2.0 Protected Resource Metadata advertised for this
	// MCP server. When set (with an openid-connect provider), it lowers into an
	// ai-mcp-oauth2 plugin.
	Metadata *MCPProtectedResourceMetadata `yaml:"metadata,omitempty"`
}

// mcpAccessFields mirrors MCPAccess without its UnmarshalYAML, so the decoder
// can populate the current keys without recursing.
type mcpAccessFields MCPAccess

// UnmarshalYAML decodes an MCPAccess, folding the deprecated
// identity_providers key into AuthStrategies.
func (a *MCPAccess) UnmarshalYAML(node *yaml.Node) error {
	var fields mcpAccessFields
	if err := node.Decode(&fields); err != nil {
		return err
	}
	*a = MCPAccess(fields)
	refs, err := appendDeprecatedAuthStrategyRefs(node, a.AuthStrategies)
	if err != nil {
		return err
	}
	a.AuthStrategies = refs
	return nil
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
