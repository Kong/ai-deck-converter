//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// testOpenAIDecisionsCapability proves an OpenAI model with the decisions
// capability serves POST <base>/decisions. The request reaches the mock
// upstream with the provider credential and the target model name.
func testOpenAIDecisionsCapability(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "openai_decisions_capability", "2.2", patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.2.0"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	t.Run("route loaded", func(t *testing.T) {
		requireRouteProtocols(t, gateway, "openai-decisions", []string{"http", "https"})
	})

	t.Run("decisions request is proxied", func(t *testing.T) {
		resp := httpPost(t, gateway.ProxyURL()+"/ai-decisions/decisions",
			map[string]string{"Content-Type": "application/json"},
			`{"model": "gpt-5.2-decisions", "messages": [{"role": "user", "content": "Approve the refund?"}]}`)
		requireStatus(t, resp, 200, "decisions request")
		if !strings.Contains(resp.Body, "hello from the mock upstream") {
			t.Fatalf("response did not come from the mock upstream:\n%s", resp.Body)
		}

		hits := mock.hits()
		if len(hits) != 1 {
			t.Fatalf("expected 1 request at the mock upstream, saw %d", len(hits))
		}
		if hits[0].Authorization != mockAuthHeaderValue {
			t.Fatalf("mock request carried Authorization %q, want %q", hits[0].Authorization, mockAuthHeaderValue)
		}
		if !strings.Contains(hits[0].Body, `"gpt-5.2"`) {
			t.Fatalf("mock request body does not carry the target model gpt-5.2:\n%s", hits[0].Body)
		}
		t.Logf("PASS: decisions request reached the mock upstream at %s", hits[0].Path)
	})
}
