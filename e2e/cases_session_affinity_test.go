//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	mockTargetAContent = "hello from mock target A"
	mockTargetBContent = "hello from mock target B"
)

// testSessionAffinitySticksToTarget proves the gateway keeps one chat
// conversation on the target that served its first turn. The model sets
// config.balancer.session_affinity. The converter emits that as the
// ai-proxy-advanced conversation_state block.
func testSessionAffinitySticksToTarget(t *testing.T, license string) {
	targetA := startMockUpstreamWithContent(t, "mock-target-a", mockTargetAContent)
	targetB := startMockUpstreamWithContent(t, "mock-target-b", mockTargetBContent)

	configPath := convertCase(t, "session_affinity_sticks_to_target", "2.4",
		patchSessionAffinityTargets(targetA.Port, targetB.Port))

	gateway := startGateway(t, GatewayOptions{
		// TODO: remove this note once kong/kong-ai-gateway:2.4.0 is published.
		// shortcut: 2.4.0 is not published yet, run with
		// AI_GATEWAY_IMAGE=kong/kong-ai-gateway-dev:aigw-master until it is.
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.4.0"),
		Config: configPath,
		// The round-robin pointer is per worker. One worker makes the control
		// turns cover both targets on every run.
		Env: []string{licenseEnv(license), "KONG_NGINX_WORKER_PROCESSES=1"},
	})
	gateway.waitReady()

	url := gateway.ProxyURL() + "/sticky/chat/completions"
	headers := map[string]string{"Content-Type": "application/json"}
	// A conversation state key needs a history prefix of 1024 bytes or more.
	// A shorter first question would have no key and no affinity.
	longQuestion := strings.Repeat("Tell me about gateway routing. ", 50)

	firstTurn := httpPost(t, url, headers, chatRequest(t, []chatMessage{
		{Role: "user", Content: longQuestion},
	}))
	requireStatus(t, firstTurn, 200, "first turn")

	// Round-robin starts from either target. The test follows the mock that
	// served the first turn.
	served, other := targetA, targetB
	servedContent := mockTargetAContent
	if len(targetA.hits()) == 0 && len(targetB.hits()) == 1 {
		served, other = targetB, targetA
		servedContent = mockTargetBContent
	}
	requireHits(t, "after the first turn", served, other, 1, 0)

	secondTurn := httpPost(t, url, headers, chatRequest(t, []chatMessage{
		{Role: "user", Content: longQuestion},
		{Role: "assistant", Content: servedContent},
		{Role: "user", Content: "And a follow-up."},
	}))
	requireStatus(t, secondTurn, 200, "second turn")
	if !strings.Contains(secondTurn.Body, servedContent) {
		t.Fatalf("the second turn left the target that served the first turn:\n%s", secondTurn.Body)
	}
	requireHits(t, "after the second turn", served, other, 2, 0)

	// A conversation with no stored key goes back to round-robin. Round-robin
	// covers both targets over several turns.
	for i := 0; i < 10; i++ {
		control := httpPost(t, url, headers, chatRequest(t, []chatMessage{
			{Role: "user", Content: fmt.Sprintf("A separate question %d.", i)},
		}))
		requireStatus(t, control, 200, fmt.Sprintf("control turn %d", i))
	}
	if got := len(other.hits()); got == 0 {
		t.Fatalf("round-robin never used the other target in 10 control turns")
	}

	t.Logf("PASS: the second turn stayed on the first target and new conversations used both targets")
}

// testSessionAffinityRedisConfig proves a real gateway accepts a redis session
// affinity storage written with the nested API blocks. The plugin schema has the
// flat keepalive fields only.
func testSessionAffinityRedisConfig(t *testing.T, license string) {
	redisPort := startRedis(t)
	targetA := startMockUpstreamWithContent(t, "mock-target-a", mockTargetAContent)
	targetB := startMockUpstreamWithContent(t, "mock-target-b", mockTargetBContent)

	configPath := convertCase(t, "session_affinity_redis", "2.4",
		patchSessionAffinityRedisConfig(targetA.Port, targetB.Port, redisPort))

	gateway := startGateway(t, GatewayOptions{
		Image: gatewayImage(t, "kong/kong-ai-gateway:2.4.0"),
		// shortcut: 2.4.0 is not published yet, run with
		// AI_GATEWAY_IMAGE=kong/kong-ai-gateway-dev:aigw-master until it is.
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	status, _, body := httpGet(gateway.AdminURL() + "/plugins?size=1000")
	if status != 200 {
		t.Fatalf("GET /plugins returned %d:\n%s", status, body)
	}
	var plugins struct {
		Data []struct {
			Name   string `json:"name"`
			Config struct {
				ConversationState map[string]any `json:"conversation_state"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &plugins); err != nil {
		t.Fatalf("decoding /plugins: %v", err)
	}

	for _, plugin := range plugins.Data {
		if plugin.Name != "ai-proxy-advanced" {
			continue
		}
		state := plugin.Config.ConversationState
		redis, _ := state["redis"].(map[string]any)
		if redis == nil {
			t.Fatalf("conversation_state has no redis block: %#v", state)
		}
		if got := redis["keepalive_pool_size"]; got != float64(256) {
			t.Fatalf("conversation_state.redis.keepalive_pool_size = %v, want 256", got)
		}
		if got := redis["keepalive_backlog"]; got != float64(20) {
			t.Fatalf("conversation_state.redis.keepalive_backlog = %v, want 20", got)
		}
		if nested, ok := redis["keepalive"]; ok {
			t.Fatalf("conversation_state.redis kept a nested keepalive block: %#v", nested)
		}
		if got := redis["host"]; got != "host.docker.internal" {
			t.Fatalf("conversation_state.redis.host = %v, want the test redis", got)
		}
		t.Logf("PASS: the gateway accepted the redis session affinity config with flat keepalive fields")
		return
	}
	t.Fatalf("no ai-proxy-advanced plugin in the loaded config")
}

// startRedis runs a Redis container for the affinity store and returns its
// published port. The gateway reaches it through host.docker.internal.
func startRedis(t *testing.T) int {
	t.Helper()

	d := dockerCLI{t: t}
	name := fmt.Sprintf("aidc-e2e-redis-%d", time.Now().UnixNano()%1e6)
	d.remove(name)
	port := freePort(t)
	d.run("run", "-d", "--name", name, "-p", fmt.Sprintf("%d:6379", port), "redis:7-alpine")
	t.Cleanup(func() { d.remove(name) })

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if out, err := execCommand("docker", "exec", name, "redis-cli", "ping"); err == nil &&
			strings.Contains(out, "PONG") {
			return port
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("redis did not become ready\n%s", d.run("logs", name))
	return 0
}

// patchSessionAffinityRedisConfig points the targets at the mocks and the redis
// storage at the test container.
func patchSessionAffinityRedisConfig(firstPort, secondPort, redisPort int) func([]byte) ([]byte, error) {
	targets := patchSessionAffinityTargets(firstPort, secondPort)
	return func(src []byte) ([]byte, error) {
		patched, err := targets(src)
		if err != nil {
			return nil, err
		}

		var doc yaml.Node
		if err := yaml.Unmarshal(patched, &doc); err != nil {
			return nil, fmt.Errorf("parsing patched input.yaml: %w", err)
		}
		models := mappingChild(&doc, "models")
		if models == nil || models.Kind != yaml.SequenceNode || len(models.Content) == 0 {
			return nil, errors.New("input.yaml has no models list")
		}
		storage := mappingChild(mappingChild(mappingChild(models.Content[0], "config"), "balancer"),
			"session_affinity")
		if storage != nil {
			storage = mappingChild(storage, "storage")
		}
		if storage == nil {
			return nil, errors.New("the model has no session affinity storage")
		}
		setScalar(storage, "host", "host.docker.internal")
		setScalar(storage, "port", fmt.Sprint(redisPort))
		// The plugin schema requires an integer port. setScalar writes strings.
		if port := mappingChild(storage, "port"); port != nil {
			port.Tag = "!!int"
		}
		return remarshal(&doc)
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest builds a chat completion request for the sticky-model. The
// ai-model-selector reads the alias from the body `model` field.
func chatRequest(t *testing.T, messages []chatMessage) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model":    "sticky-model",
		"messages": messages,
	})
	if err != nil {
		t.Fatalf("building the chat request: %v", err)
	}
	return string(body)
}

func requireHits(t *testing.T, stage string, served, other *mockUpstream, wantServed, wantOther int) {
	t.Helper()
	if got := len(served.hits()); got != wantServed {
		t.Fatalf("%s: the serving target got %d request(s), want %d", stage, got, wantServed)
	}
	if got := len(other.hits()); got != wantOther {
		t.Fatalf("%s: the other target got %d request(s), want %d", stage, got, wantOther)
	}
}

// patchSessionAffinityTargets points the two model targets at the mocks. It also
// replaces the vault reference with a fixed value.
func patchSessionAffinityTargets(firstPort, secondPort int) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return nil, fmt.Errorf("parsing input.yaml: %w", err)
		}

		models := mappingChild(&doc, "models")
		if models == nil || models.Kind != yaml.SequenceNode || len(models.Content) == 0 {
			return nil, errors.New("input.yaml has no models list")
		}
		targets := mappingChild(models.Content[0], "targets")
		if targets == nil || targets.Kind != yaml.SequenceNode || len(targets.Content) != 2 {
			return nil, errors.New("the model does not have two targets")
		}
		for i, port := range []int{firstPort, secondPort} {
			setScalar(mappingChild(targets.Content[i], "config"), "upstream_url",
				fmt.Sprintf("http://host.docker.internal:%d", port))
		}

		providers := mappingChild(&doc, "model_providers")
		if providers == nil || providers.Kind != yaml.SequenceNode {
			return nil, errors.New("input.yaml has no model_providers list")
		}
		for _, provider := range providers.Content {
			headers := mappingChild(mappingChild(mappingChild(provider, "config"), "auth"), "headers")
			if headers == nil || headers.Kind != yaml.SequenceNode || len(headers.Content) == 0 {
				continue
			}
			setScalar(headers.Content[0], "value", mockAuthHeaderValue)
		}
		return remarshal(&doc)
	}
}
