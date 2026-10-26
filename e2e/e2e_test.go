//go:build e2e

package e2e

import (
	"flag"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	flag.BoolVar(&flagUpdate, "update", false, "rewrite the expected.yaml snapshot of each case instead of comparing against it")
	flag.Parse()
	os.Exit(m.Run())
}

// TestE2E runs every end-to-end case against a real Kong AI Gateway container.
// Each case converts its AI Gateway input with the production converter code,
// checks the output against a checked-in snapshot, boots a gateway, and drives
// the proxy. Case fixtures live in testdata/<case>/.
//
// Run everything:    make e2e
// Run one case:      make e2e-case CASE=single_model_multiple_aliases
// Update snapshots:  make e2e-update
//
// Each case needs a Kong Enterprise license (e2e/license.json,
// KONG_LICENSE, or KONG_LICENSE_DATA) and is skipped without one. Cases run
// sequentially by default because each boots a full gateway; set
// AIDC_E2E_PARALLEL=1 to run them concurrently (each gets its own container,
// free ports, and mock upstreams, but three concurrent gateways need a
// Docker VM with enough memory).
func TestE2E(t *testing.T) {
	license := requireLicense(t)

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"single_model_multiple_aliases", func(t *testing.T) { testSingleModelMultipleAliases(t, license) }},
		{"pre_function_model_ordering", func(t *testing.T) { testPreFunctionModelPolicyRunsAfterModelSelection(t, license) }},
		{"pre_function_model_ordering_auth", func(t *testing.T) { testPreFunctionModelOrderingKeepsAuthFirst(t, license) }},
		{"model_scoped_request_termination_auth", func(t *testing.T) { testModelScopedRequestTermination(t, license) }},
		{"gemini_model_all_capabilities", func(t *testing.T) { testGeminiModelAllCapabilities(t, license) }},
		{"bedrock_model_all_endpoints", func(t *testing.T) { testBedrockModelAllEndpoints(t, license) }},
		{"gemini_interactions_api", func(t *testing.T) { testGeminiInteractionsAPI(t, license) }},
		{"reusable_toolsets_are_internal_only", func(t *testing.T) { testReusableToolsetsAreInternalOnly(t, license) }},
		{"token_vault_gates_tools_and_enrolls", func(t *testing.T) { testTokenVaultGatesToolsAndEnrolls(t, license) }},
		{"realtime_model_websocket_transport", func(t *testing.T) { testRealtimeModelWebSocketTransport(t, license) }},
		{"wildcard_model_all_endpoints_2_0", func(t *testing.T) {
			testWildcardModelAllEndpoints(t, license, "wildcard_model_all_endpoints_2_0", "2.0", "kong/kong-ai-gateway:2.0.3")
		}},
		{"wildcard_model_all_endpoints_2_1", func(t *testing.T) {
			testWildcardModelAllEndpoints(t, license, "wildcard_model_all_endpoints_2_1", "2.1", "kong/kong-ai-gateway:2.1.0")
		}},
		{"wildcard_model_all_endpoints_2_2", func(t *testing.T) {
			testWildcardModelAllEndpoints(t, license, "wildcard_model_all_endpoints_2_2", "2.2", "kong/kong-ai-gateway:2.2.0")
		}},
		{"wildcard_model_all_endpoints_2_3", func(t *testing.T) {
			testWildcardModelAllEndpoints(t, license, "wildcard_model_all_endpoints_2_3", "2.3",
				"kong/kong-ai-gateway-dev:2.3.0-rc.1")
		}},
		{"token_vault_exchange_credential_placement", func(t *testing.T) { testTokenVaultExchangeCredentialPlacement(t, license) }},
		{"token_vault_callback_oidc_enrollment", func(t *testing.T) { testTokenVaultCallbackOIDCEnrollment(t, license) }},
		{"token_vault_manager_session_enrollment", func(t *testing.T) { testTokenVaultManagerSessionEnrollment(t, license) }},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if os.Getenv("AIDC_E2E_PARALLEL") == "1" {
				t.Parallel()
			}
			c.run(t)
		})
	}
}

// tokenVaultImage is an aigw-master build that carries the Token Vault
// plugins, which merged in kong/kong-ee#21787. A released tag can replace
// this once one ships.
const tokenVaultImage = "kong/kong-ai-gateway-dev:40195361547-9c956dfc8efa257e040aaf99cbb6a4c64fa8664d-ubuntu"

func gatewayImage(t *testing.T, fallback string) string {
	t.Helper()
	if v := os.Getenv("AI_GATEWAY_IMAGE"); v != "" {
		return v
	}
	return fallback
}
