//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// testAgentTargetsFailover proves that the gateway sends agent traffic to a
// failover target when the only regular target is down.
func testAgentTargetsFailover(t *testing.T, license string) {
	backup := startMockUpstream(t)
	closedPort := freePort(t)

	configPath := convertCase(t, "agent_targets_failover", "2.4",
		patchAgentTargets(closedPort, backup.Port))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.4.0"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	url := gateway.ProxyURL() + "/booking/agent"
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, _, body := httpGet(url)
		if status == http.StatusOK && len(backup.hits()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the failover target got no traffic: last status %d, body %s\n%s",
				status, body, gateway.logs(50))
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got := backup.hits()[0].Path; got != "/agent" {
		t.Fatalf("the failover target got path %q, want /agent", got)
	}
	t.Logf("PASS: the failover target got traffic after the regular target failed")
}

// patchAgentTargets sets the ports of the two targets of the first agent.
func patchAgentTargets(regularPort, failoverPort int) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return nil, fmt.Errorf("parsing input.yaml: %w", err)
		}
		agents := mappingChild(&doc, "agents")
		if agents == nil || agents.Kind != yaml.SequenceNode || len(agents.Content) == 0 {
			return nil, errors.New("input.yaml has no agents list")
		}
		targets := mappingChild(mappingChild(agents.Content[0], "config"), "targets")
		if targets == nil || targets.Kind != yaml.SequenceNode || len(targets.Content) != 2 {
			return nil, errors.New("the first agent does not have two targets")
		}
		setScalar(targets.Content[0], "address", fmt.Sprintf("host.docker.internal:%d", regularPort))
		setScalar(targets.Content[1], "address", fmt.Sprintf("host.docker.internal:%d", failoverPort))
		return remarshal(&doc)
	}
}
