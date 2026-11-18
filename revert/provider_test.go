package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
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
	require.Equal(t, "session-token", bedrock.AWSSessionToken)
	require.Empty(t, bedrock.SessionToken)

	sagemaker, _ := defoldAuth(auth, "sagemaker")
	require.Equal(t, "sagemaker", sagemaker.Type)
	require.Equal(t, "session-token", sagemaker.SessionToken)
	require.Empty(t, sagemaker.AWSSessionToken)
}

func TestDefoldAuthBedrockOverridePlaceholder(t *testing.T) {
	auth := map[string]any{
		"allow_override": true,
		"header_name":    aimap.BedrockOverrideHeaderName,
		"header_value":   aimap.BedrockOverrideHeaderValue,
	}

	bedrock, override := defoldAuth(auth, "bedrock")
	require.Empty(t, bedrock.Headers)
	require.Empty(t, bedrock.Type)
	require.True(t, *override)

	openai, _ := defoldAuth(auth, "openai")
	require.Equal(t, []aigw.AuthHeader{{
		Name: aimap.BedrockOverrideHeaderName, Value: aimap.BedrockOverrideHeaderValue,
	}}, openai.Headers)
}
