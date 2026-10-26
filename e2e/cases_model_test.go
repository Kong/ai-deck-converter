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
	configPath := convertCase(t, "single_model_multiple_aliases", "2.0", patchMockUpstream(mock.Port, mockAuthHeaderValue))

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

// testPreFunctionModelPolicyRunsAfterModelSelection proves that a model
// policy whose priority outranks ai-model-selector still runs for its model.
// The route's ai-model-selector carries the ordering Kong reads before the
// model is known. A model-scoped plugin's own ordering is never read.
func testPreFunctionModelPolicyRunsAfterModelSelection(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "pre_function_model_ordering", "2.0", patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	chat := func(t *testing.T, alias string) httpResponse {
		t.Helper()
		return httpPost(t, gateway.ProxyURL()+"/ai-ordering-probe/chat/completions",
			map[string]string{"Content-Type": "application/json"},
			fmt.Sprintf(`{"model": %q, "messages": [{"role": "user", "content": "hi"}]}`, alias))
	}

	for _, alias := range []string{"ordering-probe-alias-one", "ordering-probe-alias-two"} {
		t.Run("guarded alias="+alias, func(t *testing.T) {
			resp := chat(t, alias)
			requireStatus(t, resp, 200, fmt.Sprintf("alias %q", alias))
			var probe struct {
				SelectedModel *string `json:"selected_model"`
			}
			if err := json.Unmarshal([]byte(resp.Body), &probe); err != nil || probe.SelectedModel == nil {
				t.Fatalf("alias %q: expected the probe's selected_model JSON, got:\n%s", alias, resp.Body)
			}
			if *probe.SelectedModel != alias {
				t.Fatalf("alias %q: probe saw ngx.ctx.ai_model %q, want %q", alias, *probe.SelectedModel, alias)
			}
			t.Logf("PASS: alias %q ran the model-scoped pre-function after model selection", alias)
		})
	}

	if hits := mock.hits(); len(hits) != 0 {
		t.Fatalf("guarded aliases must not reach the mock upstream, saw %d request(s)", len(hits))
	}

	t.Run("unguarded alias=unguarded-alias", func(t *testing.T) {
		resp := chat(t, "unguarded-alias")
		requireStatus(t, resp, 200, `alias "unguarded-alias"`)
		if !strings.Contains(resp.Body, "hello from the mock upstream") {
			t.Fatalf("unguarded alias: expected the mock upstream's response (the probe is scoped to the other model), got:\n%s",
				resp.Body)
		}
		t.Logf("PASS: the model-scoped pre-function did not run for a model without the policy")
	})
}

// testPreFunctionModelOrderingKeepsAuthFirst proves that reordering keeps
// authentication ahead of ai-model-selector and the model policy. An
// unauthenticated request gets the 401 and never reaches the pre-function.
func testPreFunctionModelOrderingKeepsAuthFirst(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "pre_function_model_ordering_auth", "2.0", patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	chat := func(t *testing.T, apiKey string) httpResponse {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json"}
		if apiKey != "" {
			headers["x-api-key"] = apiKey
		}
		return httpPost(t, gateway.ProxyURL()+"/ai-auth-ordering-probe/chat/completions", headers,
			`{"model": "auth-ordering-probe-alias", "messages": [{"role": "user", "content": "hi"}]}`)
	}

	for name, apiKey := range map[string]string{"no key": "", "wrong key": "not-the-key"} {
		t.Run("unauthenticated "+name, func(t *testing.T) {
			resp := chat(t, apiKey)
			requireStatus(t, resp, 401, name)
			if strings.Contains(resp.Body, "selected_model") {
				t.Fatalf("%s: the model-scoped pre-function ran before authentication rejected the request:\n%s",
					name, resp.Body)
			}
			t.Logf("PASS: %s was rejected before the model-scoped pre-function ran", name)
		})
	}

	t.Run("authenticated", func(t *testing.T) {
		resp := chat(t, "ordering-probe-secret")
		requireStatus(t, resp, 200, "valid key")
		var probe struct {
			SelectedModel *string `json:"selected_model"`
		}
		if err := json.Unmarshal([]byte(resp.Body), &probe); err != nil || probe.SelectedModel == nil {
			t.Fatalf("valid key: expected the probe's selected_model JSON, got:\n%s", resp.Body)
		}
		if *probe.SelectedModel != "auth-ordering-probe-alias" {
			t.Fatalf("valid key: probe saw ngx.ctx.ai_model %q, want %q", *probe.SelectedModel, "auth-ordering-probe-alias")
		}
		t.Logf("PASS: an authenticated request ran the model-scoped pre-function after model selection")
	})

	if hits := mock.hits(); len(hits) != 0 {
		t.Fatalf("no request must reach the mock upstream, saw %d request(s)", len(hits))
	}
}

// testModelScopedRequestTermination proves that a model-scoped
// request-termination policy runs on a route whose selector has an ordering.
// The route has one auth strategy, so key-auth rejects the request itself.
// request-termination then stays out of the ordering and runs after the model
// is known.
func testModelScopedRequestTermination(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "model_scoped_request_termination_auth", "2.0",
		patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	chat := func(t *testing.T, apiKey, alias string) httpResponse {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json"}
		if apiKey != "" {
			headers["x-api-key"] = apiKey
		}
		return httpPost(t, gateway.ProxyURL()+"/ai-termination-probe/chat/completions", headers,
			fmt.Sprintf(`{"model": %q, "messages": [{"role": "user", "content": "hi"}]}`, alias))
	}

	for name, apiKey := range map[string]string{"no key": "", "wrong key": "not-the-key"} {
		for _, alias := range []string{"probed-alias", "blocked-alias", "open-alias"} {
			t.Run("unauthenticated "+name+" alias="+alias, func(t *testing.T) {
				resp := chat(t, apiKey, alias)
				requireStatus(t, resp, 401, name)
				if strings.Contains(resp.Body, "selected_model") || strings.Contains(resp.Body, "model blocked") {
					t.Fatalf("%s: a model-scoped policy ran before key-auth rejected the request:\n%s", name, resp.Body)
				}
				t.Logf("PASS: %s was rejected by key-auth before any model-scoped policy ran", name)
			})
		}
	}

	t.Run("authenticated alias=blocked-alias", func(t *testing.T) {
		resp := chat(t, "termination-probe-secret", "blocked-alias")
		requireStatus(t, resp, 403, "blocked alias")
		if !strings.Contains(resp.Body, "model blocked") {
			t.Fatalf("blocked alias: expected the request-termination message, got:\n%s", resp.Body)
		}
		t.Logf("PASS: the model-scoped request-termination ran after model selection")
	})

	t.Run("authenticated alias=probed-alias", func(t *testing.T) {
		resp := chat(t, "termination-probe-secret", "probed-alias")
		requireStatus(t, resp, 200, "probed alias")
		var probe struct {
			SelectedModel *string `json:"selected_model"`
		}
		if err := json.Unmarshal([]byte(resp.Body), &probe); err != nil || probe.SelectedModel == nil {
			t.Fatalf("probed alias: expected the probe's selected_model JSON, got:\n%s", resp.Body)
		}
		if *probe.SelectedModel != "probed-alias" {
			t.Fatalf("probed alias: probe saw ngx.ctx.ai_model %q, want %q", *probe.SelectedModel, "probed-alias")
		}
		t.Logf("PASS: the model-scoped pre-function still ran after model selection")
	})

	t.Run("authenticated alias=open-alias", func(t *testing.T) {
		resp := chat(t, "termination-probe-secret", "open-alias")
		requireStatus(t, resp, 200, "open alias")
		if !strings.Contains(resp.Body, "hello from the mock upstream") {
			t.Fatalf("open alias: expected the mock upstream's response, got:\n%s", resp.Body)
		}
		t.Logf("PASS: a model without policies on the same route reached the upstream")
	})

	if hits := mock.hits(); len(hits) != 1 {
		t.Fatalf("only the open alias must reach the mock upstream, saw %d request(s)", len(hits))
	}
}

// testGeminiModelAllCapabilities proves the data plane accepts a gemini model
// declaring every capability the gemini section serves, which renders on both
// the Gemini Standard API and the Gemini Enterprise AI paths. A DB-less gateway refuses to start on
// a declarative config it rejects, so readiness alone shows acceptance; the
// admin API check then confirms every route was actually loaded.
func testGeminiModelAllCapabilities(t *testing.T, license string) {
	configPath := convertCase(t, "gemini_model_all_capabilities", "2.0", nil)

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	loaded := requireRoutesLoaded(t, gateway,
		"gemini-generate", "gemini-enterprise-generate",
		"gemini-embeddings", "gemini-enterprise-embeddings",
		"gemini-predict", "gemini-predict-long-running",
		"gemini-ranking",
		"gemini-batches", "gemini-enterprise-batches",
		"gemini-files",
	)
	t.Logf("PASS: the gateway accepted all %d gemini capability routes", loaded)
}

// testGeminiInteractionsAPI proves the data plane accepts gemini targets on
// llm/v1/responses in the gemini format. It fails until a data plane release
// ships the Gemini Interactions API. Earlier images reject the config with
// "gemini does not support llm/v1/responses API".
func testGeminiInteractionsAPI(t *testing.T, license string) {
	configPath := convertCase(t, "gemini_interactions_api", "2.4", nil)

	// TODO update the gateway image so that this test passes
	// Gemini interactions API support is planned for AI Gateway 2.4, which is not yet available
	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway-dev:39946325377-167b5f34f5ae4ac4be1782b22b9f250caefa76f5"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	loaded := requireRoutesLoaded(t, gateway,
		"gemini-responses", "gemini-beta-responses",
		"gemini-enterprise-responses", "gemini-enterprise-beta-responses",
	)
	t.Logf("PASS: the gateway accepted all %d gemini interactions routes", loaded)
}

// testBedrockModelAllEndpoints proves the data plane accepts every bedrock
// endpoint and every bedrock mantle endpoint. A mantle target serves only the
// openai chat, responses, and files endpoints and the anthropic messages
// endpoint.
func testBedrockModelAllEndpoints(t *testing.T, license string) {
	configPath := convertCase(t, "bedrock_model_all_endpoints", "2.2", nil)

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway-dev:39781160847-8e8728ff5c40091224a4182628fac5a80f6b29dc"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	loaded := requireRoutesLoaded(t, gateway,
		"bedrock-converse", "bedrock-invoke", "bedrock-chat", "bedrock-messages",
		"bedrock-retrieve", "bedrock-rerank",
		"bedrock-invoke-2", "bedrock-invoke-3", "bedrock-invoke-4",
		"bedrock-batches",
		"openai-chat", "openai-responses", "anthropic-messages", "openai-files",
	)
	t.Logf("PASS: the gateway accepted all %d bedrock and bedrock mantle routes", loaded)
}

// requireRoutesLoaded fails t unless the gateway loaded each route in names.
// It returns the count of loaded routes.
func requireRoutesLoaded(t *testing.T, gateway *Gateway, names ...string) int {
	t.Helper()
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
	for _, name := range names {
		if !loaded[name] {
			t.Fatalf("route %q was not loaded by the gateway; loaded routes: %v", name, loaded)
		}
	}
	return len(routes.Data)
}
