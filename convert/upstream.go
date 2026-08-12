package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// upstreamAuthBlock lowers config.upstream into the `auth` record shared by the
// ai-a2a-proxy and ai-mcp-proxy plugins. It returns nil when no upstream auth
// is set, so the plugin's `provider: off` default stays implicit and the output
// remains minimal. Reversed by revert's upstreamFromConfig.
//
// entity is used only for warning messages (e.g. `agent "foo"`).
func (c *Converter) upstreamAuthBlock(u *aigw.UpstreamConfig, entity string) (map[string]any, error) {
	if u == nil || u.Auth == nil {
		return nil, nil
	}
	a := u.Auth
	if a.Type != aimap.UpstreamAuthTypeAWS {
		return nil, c.warn("%s: unsupported upstream auth type %q; only %q is supported",
			entity, a.Type, aimap.UpstreamAuthTypeAWS)
	}

	// Map the AI Gateway field names to their plugin aws_iam counterparts via
	// the shared aimap table so forward and reverse can't drift.
	values := map[string]string{
		"access_key_id":     a.AccessKeyID,
		"secret_access_key": a.SecretAccessKey,
		"session_token":     a.SessionToken,
		"region":            a.Region,
		"assume_role_arn":   a.AssumeRoleARN,
		"role_session_name": a.RoleSessionName,
		"sts_endpoint_url":  a.STSEndpointURL,
	}
	awsIAM := map[string]any{}
	for _, k := range aimap.AWSIAMAuthKeys {
		setIfNotEmpty(awsIAM, k.Plugin, values[k.AIGW])
	}

	auth := map[string]any{"provider": aimap.UpstreamAuthProviderAWSIAM}
	if len(awsIAM) > 0 {
		auth["aws_iam"] = awsIAM
	}
	return auth, nil
}
