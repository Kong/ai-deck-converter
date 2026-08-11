package revert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A bearer_token in the plugin aws_iam block has no AI Gateway representation:
// revert warns and drops it, but still recovers the rest of the auth block.
func TestRevertUpstreamAuthBearerTokenWarns(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
services:
  - name: secure-mcp
    url: https://mcp.internal
    routes:
      - name: secure-mcp-route
        paths:
          - /mcp/secure
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: passthrough-listener
              auth:
                provider: aws_iam
                aws_iam:
                  aws_region: us-east-1
                  bearer_token: "{vault://env/bedrock-bearer}"
`)

	out, warnings, err := Revert(src, Options{})
	require.NoError(t, err)
	require.Contains(t, strings.Join(warnings, "\n"), "bearer_token")
	require.Contains(t, string(out), "region: us-east-1", "the rest of the auth block must survive")
	require.NotContains(t, string(out), "bearer_token", "bearer_token has no AI Gateway field")

	_, _, err = Revert(src, Options{Strict: true})
	require.Error(t, err, "dropping bearer_token must be fatal in strict mode")
}

// An unrecognized upstream auth provider warns and drops the auth block.
func TestRevertUpstreamAuthUnknownProvider(t *testing.T) {
	src := []byte(`
_format_version: "3.0"
services:
  - name: secure-mcp
    url: https://mcp.internal
    routes:
      - name: secure-mcp-route
        paths:
          - /mcp/secure
        plugins:
          - name: ai-mcp-proxy
            config:
              mode: passthrough-listener
              auth:
                provider: gcp_iam
`)

	out, warnings, err := Revert(src, Options{})
	require.NoError(t, err)
	require.Contains(t, strings.Join(warnings, "\n"), "unrecognized upstream auth provider")
	require.NotContains(t, string(out), "upstream:", "unknown provider must not produce an upstream block")

	_, _, err = Revert(src, Options{Strict: true})
	require.Error(t, err)
}
