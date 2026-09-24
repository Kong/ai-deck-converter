//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordedRequest struct {
	Path          string
	Authorization string
	Body          string
}

type mockUpstream struct {
	t    *testing.T
	URL  string
	Port int

	mu       sync.Mutex
	requests []recordedRequest
}

// startMockUpstream serves a generic 200 JSON response for every request and
// records what it received, so a case can assert the gateway proxied to it and
// which credential was applied.
func startMockUpstream(t *testing.T) *mockUpstream {
	t.Helper()
	m := &mockUpstream{t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.requests = append(m.requests, recordedRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          string(body),
		})
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Mock-Upstream", "ai-deck-converter-e2e")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"mock-chatcmpl","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello from the mock upstream"}}]}`))
	})

	m.Port = serveOnFreePort(t, mux)
	m.URL = fmt.Sprintf("http://host.docker.internal:%d", m.Port)
	return m
}

func (m *mockUpstream) hits() []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedRequest(nil), m.requests...)
}

func serveOnFreePort(t *testing.T, handler http.Handler) int {
	t.Helper()
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("allocating mock server port: %v", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

const (
	mockEnrollmentURL  = "https://auth.example.com/authorize?state=mock-123"
	mockExchangedCred  = "upstream-cred-123"
	mockUpstreamSessID = "mock-upstream-session-1"
)

// tokenVaultMocks stands in for the two services the token-vault case needs:
// the Kong Identity token endpoint the vault exchanges subject tokens against,
// and the upstream MCP server whose tools unlock after enrollment.
type tokenVaultMocks struct {
	IdentityURL  string
	IdentityPort int
	UpstreamURL  string
	UpstreamPort int

	enrolled atomic.Bool

	mu       sync.Mutex
	identity []recordedRequest
	authSeen []string
}

func startTokenVaultMocks(t *testing.T) *tokenVaultMocks {
	t.Helper()
	mocks := &tokenVaultMocks{}

	identityMux := http.NewServeMux()
	identityMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mocks.mu.Lock()
		mocks.identity = append(mocks.identity, recordedRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          r.Form.Get("subject_token") + "|" + r.Form.Get("resource"),
		})
		mocks.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !mocks.enrolled.Load() {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":                 "x_kong_enrollment_required",
				"x_kong_enrollment_url": mockEnrollmentURL,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":                    mockExchangedCred,
			"expires_in":                      300,
			"x-kong-bearer-methods-supported": []string{"header"},
		})
	})

	upstreamMux := http.NewServeMux()
	upstreamMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		auth := r.Header.Get("Authorization")
		mocks.mu.Lock()
		mocks.authSeen = append(mocks.authSeen, r.URL.Path+" "+auth)
		mocks.mu.Unlock()

		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONRPC(w, nil, -32700, "parse error", "")
			return
		}
		switch {
		case req.Method == "initialize":
			result := map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "mock-upstream", "version": "1.0.0"},
			}
			writeJSONRPCResult(w, req.ID, result, mockUpstreamSessID)
		case strings.HasPrefix(req.Method, "notifications/"):
			w.WriteHeader(http.StatusAccepted)
		case req.Method == "tools/list":
			result := map[string]any{
				"tools": []map[string]any{{
					"name":        "flights-search",
					"description": "Search flights (served by the mock upstream)",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			}
			writeJSONRPCResult(w, req.ID, result, "")
		case req.Method == "tools/call":
			result := map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("flights-search ok (upstream saw Authorization: %s)", auth),
				}},
			}
			writeJSONRPCResult(w, req.ID, result, "")
		default:
			writeJSONRPC(w, req.ID, -32601, "method not found: "+req.Method, "")
		}
	})

	identityPort := serveOnFreePort(t, identityMux)
	upstreamPort := serveOnFreePort(t, upstreamMux)
	mocks.IdentityPort = identityPort
	mocks.UpstreamPort = upstreamPort
	mocks.IdentityURL = fmt.Sprintf("http://host.docker.internal:%d", identityPort)
	mocks.UpstreamURL = fmt.Sprintf("http://host.docker.internal:%d", upstreamPort)
	return mocks
}

func (m *tokenVaultMocks) enroll() {
	m.enrolled.Store(true)
}

func (m *tokenVaultMocks) identityHits() []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedRequest(nil), m.identity...)
}

func writeJSONRPCResult(w http.ResponseWriter, id any, result any, session string) {
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	if session != "" {
		w.Header().Set("Mcp-Session-Id", session)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONRPC(w http.ResponseWriter, id any, code int, message, session string) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	if session != "" {
		w.Header().Set("Mcp-Session-Id", session)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
