package aigw

// UpstreamConfig mirrors the AIGatewayUpstreamConfig schema: configuration
// applied when proxying to the upstream service, carried by Agents
// (config.upstream) and MCP Servers (config.upstream). It lowers to the `auth`
// record shared by the ai-a2a-proxy and ai-mcp-proxy plugins.
type UpstreamConfig struct {
	Auth *UpstreamAuth `yaml:"auth,omitempty"`
}

// UpstreamAuth is the authentication used when proxying to the upstream. It is
// a flat union discriminated by Type (currently only "aws" / AWS SigV4),
// matching the flat-union precedent used by ProviderAuth in provider.go.
type UpstreamAuth struct {
	Type            string `yaml:"type,omitempty"` // "aws"
	AccessKeyID     string `yaml:"access_key_id,omitempty"`
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	SessionToken    string `yaml:"session_token,omitempty"`
	Region          string `yaml:"region,omitempty"`
	AssumeRoleARN   string `yaml:"assume_role_arn,omitempty"`
	RoleSessionName string `yaml:"role_session_name,omitempty"`
	STSEndpointURL  string `yaml:"sts_endpoint_url,omitempty"`
}
