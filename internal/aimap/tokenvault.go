package aimap

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
)

// Token Vault mapping shared by both directions. The AI Gateway model carries
// the Token Vault config on the MCP Server's top-level `token_vault` field; it
// lowers into the ai-mcp-proxy plugin's auth record (auth.provider:
// token_vault + auth.token_vault). Its nested Redis shape
// (AIGatewayRedisCloudConfiguration) flattens into the Kong redis-ee config
// schema's prefixed field surface. Keep every rule here so the forward
// (convert) and reverse (revert) directions can never drift.

const (
	// UpstreamAuthProviderTokenVault is the ai-mcp-proxy plugin auth.provider
	// value for Token Vault credential resolution.
	UpstreamAuthProviderTokenVault = "token_vault"

	// cloudAuthProviderPlugin is the plugin-side discriminator of a
	// cloud_authentication block; the API model calls it `type`.
	cloudAuthProviderPlugin = "auth_provider"
)

// redisCloudAuthKeys maps an AI Gateway cloud_authentication field name to its
// plugin cloud_authentication counterpart. The plugin prefixes every field
// with the provider (aws_/azure_/gcp_) and renames the discriminator
// (`type` -> `auth_provider`); the API model uses one flat, unprefixed set.
// Both convert and revert build their maps from this table so the two
// directions stay in sync.
var redisCloudAuthKeys = []struct {
	AIGW   string
	Plugin string
}{
	{AIGW: "access_key_id", Plugin: "aws_access_key_id"},
	{AIGW: "secret_access_key", Plugin: "aws_secret_access_key"},
	{AIGW: "cache_name", Plugin: "aws_cache_name"},
	{AIGW: "is_serverless", Plugin: "aws_is_serverless"},
	{AIGW: "region", Plugin: "aws_region"},
	{AIGW: "assume_role_arn", Plugin: "aws_assume_role_arn"},
	{AIGW: "role_session_name", Plugin: "aws_role_session_name"},
	{AIGW: "client_id", Plugin: "azure_client_id"},
	{AIGW: "client_secret", Plugin: "azure_client_secret"},
	{AIGW: "tenant_id", Plugin: "azure_tenant_id"},
	{AIGW: "service_account_json", Plugin: "gcp_service_account_json"},
}

// TokenVaultToPlugin lowers a TokenVaultConfig into the ai-mcp-proxy plugin's
// auth.token_vault record. Returns nil when tv is nil. Callers set
// auth.provider themselves (the token_vault block is only part of the plugin's
// auth record).
func TokenVaultToPlugin(tv *aigw.TokenVaultConfig) map[string]any {
	if tv == nil {
		return nil
	}
	block := map[string]any{}
	if tv.Directory != "" {
		block["directory"] = tv.Directory
	}
	if tv.Provider != "" {
		block["provider"] = tv.Provider
	}
	if redis := TokenVaultRedisToPlugin(tv.Redis); redis != nil {
		block["redis"] = redis
	}
	if len(tv.EncryptionSecrets) > 0 {
		block["encryption_secrets"] = append([]string(nil), tv.EncryptionSecrets...)
	}
	return block
}

// TokenVaultFromPlugin lifts the ai-mcp-proxy plugin's auth.token_vault record
// back into a TokenVaultConfig. Returns nil for an empty block. Only fields
// present in the plugin config are set, so the forward direction's omissions
// stay omissions across a round trip.
func TokenVaultFromPlugin(block map[string]any) *aigw.TokenVaultConfig {
	if len(block) == 0 {
		return nil
	}
	tv := &aigw.TokenVaultConfig{
		Directory: getAnyStr(block, "directory"),
		Provider:  getAnyStr(block, "provider"),
	}
	if redis := getAnyMap(block, "redis"); redis != nil {
		tv.Redis = TokenVaultRedisFromPlugin(redis)
	}
	for _, s := range getAnySlice(block, "encryption_secrets") {
		if str, ok := s.(string); ok {
			tv.EncryptionSecrets = append(tv.EncryptionSecrets, str)
		}
	}
	return tv
}

// TokenVaultRedisToPlugin flattens a RedisCloudConfig (the API model's nested
// AIGatewayRedisCloudConfiguration shape) into the Kong redis-ee config
// schema's flat field surface: nested keepalive/sentinel/cluster blocks become
// keepalive_*/sentinel_*/cluster_* keys, and cloud_authentication's
// discriminator and unprefixed fields are renamed to the plugin's
// auth_provider + provider-prefixed keys. Never mutates its input.
func TokenVaultRedisToPlugin(r *aigw.RedisCloudConfig) map[string]any {
	if r == nil {
		return nil
	}
	out := map[string]any{}
	setStr(out, "host", r.Host)
	setInt(out, "port", r.Port)
	setInt(out, "database", r.Database)
	setStr(out, "username", r.Username)
	setStr(out, "password", r.Password)
	setBool(out, "ssl", r.SSL)
	setBool(out, "ssl_verify", r.SSLVerify)
	setStr(out, "server_name", r.ServerName)
	setBool(out, "connection_is_proxied", r.ConnectionIsProxied)
	setInt(out, "connect_timeout", r.ConnectTimeout)
	setInt(out, "read_timeout", r.ReadTimeout)
	setInt(out, "send_timeout", r.SendTimeout)
	if r.Keepalive != nil {
		setInt(out, "keepalive_pool_size", r.Keepalive.PoolSize)
		setInt(out, "keepalive_backlog", r.Keepalive.Backlog)
	}
	if r.Sentinel != nil {
		setStr(out, "sentinel_master", r.Sentinel.Master)
		setStr(out, "sentinel_role", r.Sentinel.Role)
		setStr(out, "sentinel_username", r.Sentinel.Username)
		setStr(out, "sentinel_password", r.Sentinel.Password)
		if nodes := sentinelNodesToPlugin(r.Sentinel.Nodes); nodes != nil {
			out["sentinel_nodes"] = nodes
		}
	}
	if r.Cluster != nil {
		setInt(out, "cluster_max_redirections", r.Cluster.MaxRedirections)
		if nodes := clusterNodesToPlugin(r.Cluster.Nodes); nodes != nil {
			out["cluster_nodes"] = nodes
		}
	}
	if auth := cloudAuthToPlugin(r.CloudAuthentication); auth != nil {
		out["cloud_authentication"] = auth
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TokenVaultRedisFromPlugin lifts a flat redis-ee config block back into the
// nested RedisCloudConfig. The inverse of TokenVaultRedisToPlugin. Only fields
// present in the plugin block are set, so the forward direction's omissions
// stay omissions across a round trip.
func TokenVaultRedisFromPlugin(block map[string]any) *aigw.RedisCloudConfig {
	if len(block) == 0 {
		return nil
	}
	r := &aigw.RedisCloudConfig{
		Host:                getAnyStr(block, "host"),
		Port:                getAnyIntPtr(block, "port"),
		Database:            getAnyIntPtr(block, "database"),
		Username:            getAnyStr(block, "username"),
		Password:            getAnyStr(block, "password"),
		SSL:                 getAnyBoolPtr(block, "ssl"),
		SSLVerify:           getAnyBoolPtr(block, "ssl_verify"),
		ServerName:          getAnyStr(block, "server_name"),
		ConnectionIsProxied: getAnyBoolPtr(block, "connection_is_proxied"),
		ConnectTimeout:      getAnyIntPtr(block, "connect_timeout"),
		ReadTimeout:         getAnyIntPtr(block, "read_timeout"),
		SendTimeout:         getAnyIntPtr(block, "send_timeout"),
	}
	if block["keepalive_pool_size"] != nil || block["keepalive_backlog"] != nil {
		r.Keepalive = &aigw.RedisKeepaliveConfig{
			PoolSize: getAnyIntPtr(block, "keepalive_pool_size"),
			Backlog:  getAnyIntPtr(block, "keepalive_backlog"),
		}
	}
	master := getAnyStr(block, "sentinel_master")
	role := getAnyStr(block, "sentinel_role")
	username := getAnyStr(block, "sentinel_username")
	password := getAnyStr(block, "sentinel_password")
	sentinelNodes := getAnySlice(block, "sentinel_nodes")
	if master != "" || role != "" || username != "" || password != "" || sentinelNodes != nil {
		r.Sentinel = &aigw.RedisSentinelConfig{
			Master:   master,
			Role:     role,
			Username: username,
			Password: password,
		}
		for _, raw := range sentinelNodes {
			node, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			r.Sentinel.Nodes = append(r.Sentinel.Nodes, aigw.RedisNode{
				Host: getAnyStr(node, "host"),
				Port: getAnyIntPtr(node, "port"),
			})
		}
	}
	maxRedirections := getAnyIntPtr(block, "cluster_max_redirections")
	clusterNodes := getAnySlice(block, "cluster_nodes")
	if maxRedirections != nil || clusterNodes != nil {
		r.Cluster = &aigw.RedisClusterConfig{MaxRedirections: maxRedirections}
		for _, raw := range clusterNodes {
			node, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			r.Cluster.Nodes = append(r.Cluster.Nodes, aigw.RedisNode{
				IP:   getAnyStr(node, "ip"),
				Port: getAnyIntPtr(node, "port"),
			})
		}
	}
	r.CloudAuthentication = cloudAuthFromPlugin(getAnyMap(block, "cloud_authentication"))
	return r
}

// sentinelNodesToPlugin / clusterNodesToPlugin lower the shared RedisNode
// shape into the two node-record shapes the plugin schema distinguishes:
// sentinel nodes address by `host`, cluster nodes by `ip`.
func sentinelNodesToPlugin(nodes []aigw.RedisNode) []map[string]any {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		node := map[string]any{}
		setStr(node, "host", sentinelNodeHost(n))
		setInt(node, "port", n.Port)
		out = append(out, node)
	}
	return out
}

func clusterNodesToPlugin(nodes []aigw.RedisNode) []map[string]any {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		node := map[string]any{}
		setStr(node, "ip", clusterNodeIP(n))
		setInt(node, "port", n.Port)
		out = append(out, node)
	}
	return out
}

// cloudAuthToPlugin lowers a RedisCloudAuthentication into the plugin's
// cloud_authentication shape: `type` becomes `auth_provider`, and the variant
// fields are renamed per redisCloudAuthKeys. Only fields set on the selected
// variant are emitted.
func cloudAuthToPlugin(a *aigw.RedisCloudAuthentication) map[string]any {
	if a == nil {
		return nil
	}
	out := map[string]any{}
	setStr(out, cloudAuthProviderPlugin, a.Type)

	strs := map[string]string{
		"access_key_id":        a.AccessKeyID,
		"secret_access_key":    a.SecretAccessKey,
		"cache_name":           a.CacheName,
		"region":               a.Region,
		"assume_role_arn":      a.AssumeRoleARN,
		"role_session_name":    a.RoleSessionName,
		"client_id":            a.ClientID,
		"client_secret":        a.ClientSecret,
		"tenant_id":            a.TenantID,
		"service_account_json": a.ServiceAccountJSON,
	}
	for _, k := range redisCloudAuthKeys {
		if v, ok := strs[k.AIGW]; ok && v != "" {
			out[k.Plugin] = v
		}
	}
	setBool(out, "aws_is_serverless", a.IsServerless)
	if len(out) == 0 {
		return nil
	}
	return out
}

// cloudAuthFromPlugin lifts a plugin cloud_authentication block back into a
// RedisCloudAuthentication: `auth_provider` becomes `type`, and the
// provider-prefixed plugin keys are read back into the API model's unprefixed
// names via redisCloudAuthKeys. The inverse of cloudAuthToPlugin.
func cloudAuthFromPlugin(block map[string]any) *aigw.RedisCloudAuthentication {
	if len(block) == 0 {
		return nil
	}
	a := &aigw.RedisCloudAuthentication{Type: getAnyStr(block, cloudAuthProviderPlugin)}
	strs := map[string]*string{
		"access_key_id":        &a.AccessKeyID,
		"secret_access_key":    &a.SecretAccessKey,
		"cache_name":           &a.CacheName,
		"region":               &a.Region,
		"assume_role_arn":      &a.AssumeRoleARN,
		"role_session_name":    &a.RoleSessionName,
		"client_id":            &a.ClientID,
		"client_secret":        &a.ClientSecret,
		"tenant_id":            &a.TenantID,
		"service_account_json": &a.ServiceAccountJSON,
	}
	for _, k := range redisCloudAuthKeys {
		if ptr, ok := strs[k.AIGW]; ok {
			*ptr = getAnyStr(block, k.Plugin)
		}
	}
	a.IsServerless = getAnyBoolPtr(block, "aws_is_serverless")
	return a
}

// sentinelNodeHost / clusterNodeIP pick the address field each node list
// speaks: sentinel nodes use `host`, cluster nodes use `ip` (the plugin
// schema's split; the API model reuses one RedisNode shape for both). Each
// accepts either spelling so hand-written configs in the other convention
// still convert.
func sentinelNodeHost(n aigw.RedisNode) string {
	if n.Host != "" {
		return n.Host
	}
	return n.IP
}

func clusterNodeIP(n aigw.RedisNode) string {
	if n.IP != "" {
		return n.IP
	}
	return n.Host
}

// --- small writers/readers over decoded YAML values (yaml.v3 gives int/string/bool) ---

func setStr(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

func setInt(m map[string]any, key string, val *int) {
	if val != nil {
		m[key] = *val
	}
}

func setBool(m map[string]any, key string, val *bool) {
	if val != nil {
		m[key] = *val
	}
}

func getAnyStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getAnyMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func getAnySlice(m map[string]any, key string) []any {
	switch v := m[key].(type) {
	case []any:
		// The shape YAML decoding produces (revert's input).
		return v
	case []map[string]any:
		// The shape the forward direction's maps carry, so the two
		// directions compose directly in tests without a YAML hop.
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = e
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = e
		}
		return out
	}
	return nil
}

func getAnyIntPtr(m map[string]any, key string) *int {
	if v, ok := m[key].(int); ok {
		return &v
	}
	return nil
}

func getAnyBoolPtr(m map[string]any, key string) *bool {
	if v, ok := m[key].(bool); ok {
		return &v
	}
	return nil
}
