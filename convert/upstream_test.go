package convert

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An unsupported upstream auth type warns (non-strict) and drops the auth
// block, and is fatal under -strict.
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

	out, warnings, err := Convert(src, Options{})
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	require.Contains(t, strings.Join(warnings, "\n"), "unsupported upstream auth type")
	require.NotContains(t, string(out), "auth:", "unsupported auth must not be emitted")

	_, _, err = Convert(src, Options{Strict: true})
	require.Error(t, err, "unsupported upstream auth type must be fatal in strict mode")
}
