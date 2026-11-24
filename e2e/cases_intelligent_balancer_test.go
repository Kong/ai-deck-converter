//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	mockClassifierAuthValue = "Bearer e2e-classifier-secret"
	mockCheapContent        = "hello from gpt-4.1-mini"
	mockPremiumContent      = "hello from gpt-4.1"
)

// classifierMock stands in for the System One decisions API. It records each
// request and sends the response that respond sets.
type classifierMock struct {
	URL  string
	Port int

	mu       sync.Mutex
	requests []recordedRequest
	status   int
	answer   any
}

func startClassifierMock(t *testing.T) *classifierMock {
	t.Helper()
	m := &classifierMock{status: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.requests = append(m.requests, recordedRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          string(body),
		})
		status, answer := m.status, m.answer
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(answer)
	})
	m.Port = serveOnFreePort(t, mux)
	m.URL = fmt.Sprintf("http://host.docker.internal:%d", m.Port)
	return m
}

func (m *classifierMock) respond(status int, answer any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.answer = status, answer
}

func (m *classifierMock) hits() []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedRequest(nil), m.requests...)
}

// classifierAnswer is a System One answer that selects category.
func classifierAnswer(category string) map[string]any {
	return map[string]any{
		"model": "jev-latest",
		"usage": map[string]any{"input_tokens": 120, "output_tokens": 4},
		"answers": map[string]any{
			"task_category": map[string]any{
				"type":          "choice",
				"choice":        category,
				"confidence":    0.9,
				"probabilities": map[string]any{category: 0.9, "unclassified": 0.1},
			},
		},
	}
}

// testIntelligentBalancerRoutesByTradeoff proves the converted intelligent
// balancer end to end. The data plane asks the classifier with the converted
// credential. The cost_quality_tradeoff of each model selects its target. A
// later turn keeps its target, and a classifier failure still gets an answer.
func testIntelligentBalancerRoutesByTradeoff(t *testing.T, license string) {
	cheap := startMockUpstreamWithContent(t, "mock-cheap", mockCheapContent)
	premium := startMockUpstreamWithContent(t, "mock-premium", mockPremiumContent)
	classifier := startClassifierMock(t)
	// For debugging, the catalog ranks gpt-4.1 above gpt-4.1-mini on quality.
	// gpt-4.1-mini is cheaper. The two tradeoffs therefore select different
	// targets.
	classifier.respond(http.StatusOK, classifierAnswer("debugging"))

	configPath := convertCase(t, "intelligent_balancer_routes_by_tradeoff", "2.5",
		patchIntelligentBalancer(classifier.Port, cheap.Port, premium.Port))

	gateway := startGateway(t, GatewayOptions{
		// TODO: use a published 2.5 image once one exists.
		Image:  gatewayImage(t, "kong/kong-ai-gateway-dev:aigw-master"),
		Config: configPath,
		Env:    []string{licenseEnv(license), "CLASSIFIER_KEY=" + mockClassifierAuthValue},
	})
	gateway.waitReady()

	chat := func(route string, messages []chatMessage) httpResponse {
		t.Helper()
		body, err := json.Marshal(map[string]any{"model": route + "-model", "messages": messages})
		if err != nil {
			t.Fatalf("building the chat request: %v", err)
		}
		return httpPost(t, gateway.ProxyURL()+"/"+route+"/chat/completions",
			map[string]string{"Content-Type": "application/json"}, string(body))
	}

	t.Run("classifier request", func(t *testing.T) {
		resp := chat("cheap", []chatMessage{{Role: "user", Content: "Why does my Go test panic?"}})
		requireStatus(t, resp, 200, "cheap model")
		hits := classifier.hits()
		if len(hits) != 1 {
			t.Fatalf("the classifier got %d request(s), want 1", len(hits))
		}
		if hits[0].Path != "/v1/systemone" {
			t.Fatalf("the classifier path is %q, want the converted format /v1/systemone", hits[0].Path)
		}
		if hits[0].Authorization != mockClassifierAuthValue {
			t.Fatalf("the classifier Authorization is %q, want the converted header_value %q",
				hits[0].Authorization, mockClassifierAuthValue)
		}
		var request struct {
			Model     string         `json:"model"`
			Questions map[string]any `json:"questions"`
		}
		if err := json.Unmarshal([]byte(hits[0].Body), &request); err != nil {
			t.Fatalf("the classifier request is not JSON: %v\n%s", err, hits[0].Body)
		}
		if request.Model != "jev-latest" || request.Questions["task_category"] == nil {
			t.Fatalf("the classifier request does not name the converted model and question:\n%s", hits[0].Body)
		}
	})

	t.Run("tradeoff selects the target", func(t *testing.T) {
		for _, tc := range []struct {
			route, wantModel, wantContent string
		}{
			{"cheap", "openai/gpt-4.1-mini", mockCheapContent},
			{"premium", "openai/gpt-4.1", mockPremiumContent},
		} {
			resp := chat(tc.route, []chatMessage{{Role: "user", Content: "Find the bug in " + tc.route + "."}})
			requireStatus(t, resp, 200, tc.route)
			requireRouting(t, resp, tc.route, tc.wantModel, "debugging")
			if !strings.Contains(resp.Body, tc.wantContent) {
				t.Fatalf("%s: the answer did not come from %s:\n%s", tc.route, tc.wantModel, resp.Body)
			}
		}
	})

	t.Run("later turn keeps the target", func(t *testing.T) {
		// A session key needs a history prefix of 1024 bytes or more.
		question := strings.Repeat("Explain why this request handler leaks memory. ", 30)
		first := chat("premium", []chatMessage{{Role: "user", Content: question}})
		requireStatus(t, first, 200, "first turn")
		requireRouting(t, first, "first turn", "openai/gpt-4.1", "debugging")
		calls := len(classifier.hits())

		// For code_generation, the quality tradeoff selects gpt-4.1-mini. A
		// classified later turn would therefore change its target.
		classifier.respond(http.StatusOK, classifierAnswer("code_generation"))
		second := chat("premium", []chatMessage{
			{Role: "user", Content: question},
			{Role: "assistant", Content: mockPremiumContent},
			{Role: "user", Content: "Now write the fix."},
		})
		requireStatus(t, second, 200, "second turn")
		if got := second.Header.Get("X-Kong-LLM-Model"); got != "openai/gpt-4.1" {
			t.Fatalf("the second turn moved to %q, want the first turn target openai/gpt-4.1", got)
		}
		if got := len(classifier.hits()); got != calls {
			t.Fatalf("the second turn called the classifier again (%d calls, want %d)", got, calls)
		}
	})

	t.Run("classifier failure falls back", func(t *testing.T) {
		classifier.respond(http.StatusServiceUnavailable, map[string]any{"error": "down"})
		resp := chat("cheap", []chatMessage{{Role: "user", Content: "Another question."}})
		requireStatus(t, resp, 200, "classifier failure")
		if resp.Header.Get("X-Kong-LLM-Classifier-Error") == "" {
			t.Fatalf("the response has no X-Kong-LLM-Classifier-Error header: %v", resp.Header)
		}
		if got := resp.Header.Get("X-Kong-LLM-Task-Type"); got != "general" {
			t.Fatalf("the fallback task type is %q, want general", got)
		}
	})
}

func requireRouting(t *testing.T, resp httpResponse, context, wantModel, wantTask string) {
	t.Helper()
	if got := resp.Header.Get("X-Kong-LLM-Model"); got != wantModel {
		t.Fatalf("%s: the balancer selected %q, want %q", context, got, wantModel)
	}
	if got := resp.Header.Get("X-Kong-LLM-Task-Type"); got != wantTask {
		t.Fatalf("%s: the task type is %q, want %q", context, got, wantTask)
	}
	if got := resp.Header.Get("X-Kong-LLM-Classifier-Error"); got != "" {
		t.Fatalf("%s: the classifier failed: %s", context, got)
	}
}

// patchIntelligentBalancer points the classifier and both targets of each
// model at the mocks. It also replaces the provider vault reference with a
// fixed value.
func patchIntelligentBalancer(classifierPort, cheapPort, premiumPort int) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return nil, fmt.Errorf("parsing input.yaml: %w", err)
		}
		models := mappingChild(&doc, "models")
		if models == nil || models.Kind != yaml.SequenceNode {
			return nil, errors.New("input.yaml has no models list")
		}
		for _, model := range models.Content {
			balancer := mappingChild(mappingChild(model, "config"), "balancer")
			setScalar(mappingChild(balancer, "classifier"), "url",
				fmt.Sprintf("http://host.docker.internal:%d", classifierPort))
			targets := mappingChild(model, "targets")
			if targets == nil || targets.Kind != yaml.SequenceNode || len(targets.Content) != 2 {
				return nil, errors.New("a model does not have two targets")
			}
			for i, port := range []int{cheapPort, premiumPort} {
				setScalar(mappingChild(targets.Content[i], "config"), "upstream_url",
					fmt.Sprintf("http://host.docker.internal:%d", port))
			}
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
