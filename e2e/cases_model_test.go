//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

const mockAuthHeaderValue = "Bearer e2e-mock-secret"

// testSingleModelMultipleAliases proves each alias of a body-selector model
// routes through its own ai-proxy-advanced copy to the (mocked) OpenAI
// upstream with the provider credential applied. The converter emits one
// ai-proxy-advanced plugin and ai_models entry per alias, joined by the
// ai-gateway-model-alias-group tag.
func testSingleModelMultipleAliases(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "single_model_multiple_aliases", patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	for _, alias := range []string{"my-body-alias-value", "my-second-alias-value"} {
		t.Run("alias="+alias, func(t *testing.T) {
			resp := httpPost(t, gateway.ProxyURL()+"/ai-body-alias/chat/completions",
				map[string]string{"Content-Type": "application/json"},
				fmt.Sprintf(`{"my-model-alias": %q, "messages": [{"role": "user", "content": "Say hello in one word."}]}`, alias))

			requireStatus(t, resp, 200, fmt.Sprintf("alias %q", alias))
			if !strings.Contains(resp.Body, "hello from the mock upstream") {
				t.Fatalf("alias %q: response did not come from the mock upstream:\n%s", alias, resp.Body)
			}
			if via := headerValue(resp.Header, "Via"); !strings.Contains(strings.ToLower(via), "kong") {
				t.Fatalf("alias %q: no 'Via: ... kong' header: the response did not come through the gateway", alias)
			}
			t.Logf("PASS: alias %q proxied through the gateway to the mock upstream", alias)
		})
	}

	hits := mock.hits()
	if len(hits) < 2 {
		t.Fatalf("expected both aliases to reach the mock upstream, saw %d request(s)", len(hits))
	}
	for i, hit := range hits {
		if !strings.Contains(hit.Body, `"gpt-5.2"`) {
			t.Fatalf("mock request %d body does not carry the target model name gpt-5.2:\n%s", i+1, hit.Body)
		}
		if hit.Authorization != mockAuthHeaderValue {
			t.Fatalf("mock request %d carried Authorization %q, want %q (the provider credential must be applied to every alias)",
				i+1, hit.Authorization, mockAuthHeaderValue)
		}
	}
	t.Logf("PASS: all %d alias requests carried the provider credential to the mock upstream", len(hits))
}
