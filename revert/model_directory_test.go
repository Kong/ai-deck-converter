package revert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRevertDropsModelListRoute(t *testing.T) {
	src := []byte(`_format_version: "3.0"
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: models
        paths:
          - /v1/models
        methods:
          - GET
plugins:
  - name: ai-proxy-advanced
    route: models
    config:
      route_type: llm/v1/models
      models:
        sources:
          - plugin_id: 0f0f0f0f-1111-4111-8111-111111111111
`)

	out, warnings, err := Revert(src, Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.NotContains(t, string(out), "name: models")
}
