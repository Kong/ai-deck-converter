package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMantlePathPrefix(t *testing.T) {
	for _, name := range []string{
		"google.gemma-4-31b", "google.gemma-4-e2b", "openai.gpt-5.4", "openai.gpt-5.6-cyber",
		"openai.gpt-daybreak-blue-5.6-sol", "openai.gpt-6-astra", "openai.gpt-6.1-sol",
		"xai.grok-4.3", "xai.grok-4.6",
	} {
		require.Equal(t, "/openai/v1", MantlePathPrefix(name), name)
	}
	for _, name := range []string{
		"google.gemma-3-4b-it", "openai.gpt-oss-120b", "openai.gpt-oss-safeguard-20b",
		"deepseek.v3.1", "minimax.minimax-m2.5", "mistral.devstral-09",
		"mistral.voxtral-small-24b-2507", "moonshotai.kimi-k2.5", "nvidia.nemotron-nano-9b-v2",
		"qwen.qwen3-coder-next", "zai.glm-4.7-flash",
	} {
		require.Equal(t, "/v1", MantlePathPrefix(name), name)
	}
	for _, name := range []string{"unknown.model", "anthropic.claude-3-5-sonnet", ""} {
		require.Equal(t, MantlePathPrefixAuto, MantlePathPrefix(name), name)
	}
}

func TestMantleSupports(t *testing.T) {
	var served []string
	for _, format := range []string{"openai", "anthropic", "bedrock", "gemini"} {
		for _, ce := range SectionEndpoints(format) {
			if MantleSupports(format, ce.Spec) {
				served = append(served, format+"-"+ce.Spec.RouteLabel)
			}
		}
	}
	require.ElementsMatch(t, []string{
		"openai-chat", "openai-responses", "openai-files", "anthropic-messages",
	}, served)
}
