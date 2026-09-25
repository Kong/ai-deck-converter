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
		{"reusable_toolsets_are_internal_only", func(t *testing.T) { testReusableToolsetsAreInternalOnly(t, license) }},
		{"token_vault_gates_tools_and_enrolls", func(t *testing.T) { testTokenVaultGatesToolsAndEnrolls(t, license) }},
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

func gatewayImage(t *testing.T, fallback string) string {
	t.Helper()
	if v := os.Getenv("AI_GATEWAY_IMAGE"); v != "" {
		return v
	}
	return fallback
}
