package aimap

// Upstream-authentication mapping shared by both directions. The AI Gateway
// model carries upstream auth under `config.upstream.auth` (Agents and MCP
// Servers); it lowers to the `auth` record on the ai-a2a-proxy / ai-mcp-proxy
// plugins. Keep the type/provider constants and the field-name table here so
// the forward (convert) and reverse (revert) directions can never drift.
const (
	// UpstreamAuthTypeAWS is the AI Gateway upstream.auth.type discriminator
	// for AWS SigV4 authentication.
	UpstreamAuthTypeAWS = "aws"
	// UpstreamAuthProviderAWSIAM is the ai-a2a-proxy / ai-mcp-proxy plugin
	// auth.provider value for AWS SigV4 authentication.
	UpstreamAuthProviderAWSIAM = "aws_iam"
	// UpstreamAuthProviderOff is the plugin auth.provider default (no upstream
	// authentication). The converter never emits it, relying on the plugin
	// default instead.
	UpstreamAuthProviderOff = "off"
)

// AWSIAMAuthKey pairs an AI Gateway upstream.auth field name with its
// ai plugin auth.aws_iam counterpart.
type AWSIAMAuthKey struct {
	AIGW   string // AI Gateway upstream.auth field
	Plugin string // plugin auth.aws_iam field
}

// AWSIAMAuthKeys is the canonical field-name mapping between the AI Gateway
// upstream.auth block and the plugin auth.aws_iam block. Both convert and
// revert build their maps from this table so the two directions stay in sync.
// Note: the plugin's aws_iam block also carries a `bearer_token` field, which
// has no AI Gateway representation; revert warns and drops it.
var AWSIAMAuthKeys = []AWSIAMAuthKey{
	{AIGW: "access_key_id", Plugin: "aws_access_key_id"},
	{AIGW: "secret_access_key", Plugin: "aws_secret_access_key"},
	{AIGW: "session_token", Plugin: "aws_session_token"},
	{AIGW: "region", Plugin: "aws_region"},
	{AIGW: "assume_role_arn", Plugin: "assume_role_arn"},
	{AIGW: "role_session_name", Plugin: "role_session_name"},
	{AIGW: "sts_endpoint_url", Plugin: "sts_endpoint_url"},
}
