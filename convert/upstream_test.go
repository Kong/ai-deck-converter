package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An unsupported upstream auth type leaves out the agent.
func TestConvertUpstreamAuthUnsupportedType(t *testing.T) {
	src := []byte(`
agents:
  - type: a2a
    name: booking-agent
    config:
      url: https://booking-agent.internal/a2a
      route: {paths: [/agents/booking]}
      upstream:
        auth:
          type: gcp
          region: us-east-1
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindAgent, "booking-agent",
		"config.upstream.auth.type")
	require.Contains(t, issue.Reason, "unsupported upstream auth type")
	require.Empty(t, result.Output.Services)
}
