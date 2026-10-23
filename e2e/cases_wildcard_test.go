//go:build e2e

package e2e

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// testWildcardModelAllEndpoints proves that the data plane of one release
// loads every route of a "*" model with one target of each provider type.
// A target that the data plane rejects on a route rejects the whole
// configuration, so a loaded route list proves aimap.TargetSupports.
func testWildcardModelAllEndpoints(t *testing.T, license, caseName, minRuntimeVersion, image string) {
	configPath := convertCase(t, caseName, minRuntimeVersion, nil)

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, image),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	names := configRouteNames(t, configPath)
	loaded := requireRoutesLoaded(t, gateway, names...)
	t.Logf("PASS: the %s gateway loaded all %d routes of the wildcard models", minRuntimeVersion, loaded)
}

// configRouteNames returns the name of each route in the DB-less config at
// path.
func configRouteNames(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc struct {
		Services []struct {
			Routes []struct {
				Name string `yaml:"name"`
			} `yaml:"routes"`
		} `yaml:"services"`
		Routes []struct {
			Name string `yaml:"name"`
		} `yaml:"routes"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	var names []string
	for _, svc := range doc.Services {
		for _, r := range svc.Routes {
			names = append(names, r.Name)
		}
	}
	for _, r := range doc.Routes {
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		t.Fatalf("%s has no routes", path)
	}
	return names
}
