//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const licenseInstructions = `no Kong Enterprise license found. Provide one of:
  - a populated e2e/license.json (the "Monthly Kong Gateway Enterprise
    License" secret in 1password)
  - the KONG_LICENSE environment variable set to the license JSON
  - the KONG_LICENSE_DATA environment variable set to the license JSON`

func requireLicense(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"KONG_LICENSE", "KONG_LICENSE_DATA"} {
		if v := os.Getenv(name); v != "" && isJSON(v) {
			return v
		}
	}
	path := filepath.Join(repoRoot(t), "e2e", "license.json")
	if data, err := os.ReadFile(path); err == nil && isJSON(string(data)) {
		return string(data)
	}
	t.Skipf("skipping e2e test: %s", licenseInstructions)
	return ""
}

func isJSON(payload string) bool {
	var v any
	return json.Unmarshal([]byte(payload), &v) == nil
}

func licenseEnv(license string) string {
	return fmt.Sprintf("KONG_LICENSE_DATA=%s", license)
}
