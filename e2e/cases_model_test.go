//go:build e2e

package e2e

import (
	"encoding/json"
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

// testPreFunctionModelScopedOrderingIsNotYetEffective documents a current Kong
// limitation: a plugin carrying a model: FK can never be reordered relative to
// ai-model-selector, even with an ordering block, because Kong builds the
// ordering dependency graph once at the very start of the access phase,
// before ai-model-selector has set ngx.ctx.ai_model. A model-scoped plugin's
// own config can only resolve once ctx.ai_model is already set, so its
// ordering is invisible to that graph-building pass; it is silently skipped
// for the whole request instead of being reordered. This is the same
// catch-22 kong-ee already guards against for consumer-scoped plugins
// (kong/db/dao/plugins.lua's check_ordering_validity), just not yet for
// model scoping.
//
// The probe plugin would short-circuit with ngx.ctx.ai_model's name if it
// ran; this test instead proves it never runs at all: the mock upstream
// still receives the request (ai-proxy-advanced proxied normally) and the
// response is the mock's own canned body, not the probe's JSON. If kong-ee
// closes this gap, this test should start failing — rewrite it to assert the
// probe's selected_model instead.
func testPreFunctionModelScopedOrderingIsNotYetEffective(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "pre_function_model_ordering", patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	for _, alias := range []string{"ordering-probe-alias-one", "ordering-probe-alias-two"} {
		t.Run("alias="+alias, func(t *testing.T) {
			resp := httpPost(t, gateway.ProxyURL()+"/ai-ordering-probe/chat/completions",
				map[string]string{"Content-Type": "application/json"},
				fmt.Sprintf(`{"model": %q, "messages": [{"role": "user", "content": "hi"}]}`, alias))

			requireStatus(t, resp, 200, fmt.Sprintf("alias %q", alias))
			if !strings.Contains(resp.Body, "hello from the mock upstream") {
				t.Fatalf("alias %q: expected the mock upstream's canned response (the probe plugin "+
					"should not have run), got:\n%s", alias, resp.Body)
			}
			t.Logf("KNOWN LIMITATION: alias %q reached the mock upstream unguarded; the model-scoped "+
				"pre-function probe never ran despite its ordering block", alias)
		})
	}

	hits := mock.hits()
	if len(hits) < 2 {
		t.Fatalf("expected both aliases to reach the mock upstream, saw %d request(s)", len(hits))
	}
}

// testGeminiModelAllCapabilities proves the data plane accepts a gemini model
// declaring every capability the gemini section serves, which renders on both
// the Gemini Standard API and the Gemini Enterprise AI paths. A DB-less gateway refuses to start on
// a declarative config it rejects, so readiness alone shows acceptance; the
// admin API check then confirms every route was actually loaded.
func testGeminiModelAllCapabilities(t *testing.T, license string) {
	configPath := convertCase(t, "gemini_model_all_capabilities", nil)

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	status, _, body := httpGet(gateway.AdminURL() + "/routes")
	if status != 200 {
		t.Fatalf("GET /routes returned %d:\n%s", status, body)
	}
	var routes struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &routes); err != nil {
		t.Fatalf("decoding GET /routes: %v\n%s", err, body)
	}
	loaded := map[string]bool{}
	for _, r := range routes.Data {
		loaded[r.Name] = true
	}
	for _, name := range []string{
		"gemini-generate", "gemini-enterprise-generate",
		"gemini-embeddings", "gemini-enterprise-embeddings",
		"gemini-predict", "gemini-predict-long-running",
		"gemini-ranking",
		"gemini-batches", "gemini-enterprise-batches",
		"gemini-files",
	} {
		if !loaded[name] {
			t.Fatalf("route %q was not loaded by the gateway; loaded routes: %v", name, loaded)
		}
	}
	t.Logf("PASS: the gateway accepted all %d gemini capability routes", len(routes.Data))
}
