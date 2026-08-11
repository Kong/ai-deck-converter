package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// upstreamFromConfig lifts the `auth` record shared by the ai-a2a-proxy and
// ai-mcp-proxy plugins back into config.upstream. It returns nil when there is
// no upstream authentication (provider off or empty). It warns (the data is
// lossy) if the plugin carries a bearer_token — which has no AI Gateway
// representation — or an unrecognized provider. Reverses convert's
// upstreamAuthBlock.
//
// entity is used only for warning messages (e.g. `agent "foo"`).
func (r *Reverter) upstreamFromConfig(auth map[string]any, entity string) (*aigw.UpstreamConfig, error) {
	if len(auth) == 0 {
		return nil, nil
	}
	provider := getStr(auth, "provider")
	switch provider {
	case "", aimap.UpstreamAuthProviderOff:
		return nil, nil
	case aimap.UpstreamAuthProviderAWSIAM:
		// handled below
	default:
		return nil, r.warn("%s: unrecognized upstream auth provider %q; dropping auth", entity, provider)
	}

	awsIAM := getMap(auth, "aws_iam")
	if bearer := getStr(awsIAM, "bearer_token"); bearer != "" {
		if err := r.warn(
			"%s: upstream auth bearer_token has no AI Gateway representation; dropping it", entity); err != nil {
			return nil, err
		}
	}

	// Read plugin aws_iam fields back into AI Gateway names via the shared
	// aimap table so forward and reverse can't drift.
	values := map[string]string{}
	for _, k := range aimap.AWSIAMAuthKeys {
		if v := getStr(awsIAM, k.Plugin); v != "" {
			values[k.AIGW] = v
		}
	}
	a := &aigw.UpstreamAuth{
		Type:            aimap.UpstreamAuthTypeAWS,
		AccessKeyID:     values["access_key_id"],
		SecretAccessKey: values["secret_access_key"],
		SessionToken:    values["session_token"],
		Region:          values["region"],
		AssumeRoleARN:   values["assume_role_arn"],
		RoleSessionName: values["role_session_name"],
		STSEndpointURL:  values["sts_endpoint_url"],
	}
	return &aigw.UpstreamConfig{Auth: a}, nil
}
