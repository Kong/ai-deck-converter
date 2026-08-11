package revert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefoldAuthAWSVariants(t *testing.T) {
	auth := map[string]any{
		"aws_access_key_id":     "access-key",
		"aws_secret_access_key": "secret-key",
		"aws_session_token":     "session-token",
	}

	bedrock, _ := defoldAuth(auth, "bedrock")
	require.Equal(t, "aws", bedrock.Type)
	require.Equal(t, "session-token", bedrock.SessionToken)

	sagemaker, _ := defoldAuth(auth, "sagemaker")
	require.Equal(t, "sagemaker", sagemaker.Type)
	require.Equal(t, "session-token", sagemaker.SessionToken)
}
