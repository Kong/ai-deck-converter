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
	"testing"
	"time"
)

type recordedRequest struct {
	Path          string
	Query         string
	Authorization string
	ExtraHeaders  map[string]string
	Body          string
	WebSocket     bool
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
// which credential was applied. A WebSocket upgrade request gets an echo
// connection instead (see serveWebSocketEcho).
func startMockUpstream(t *testing.T) *mockUpstream {
	t.Helper()
	m := &mockUpstream{t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if isWebSocketUpgrade(r) {
			m.record(recordedRequest{
				Path:          r.URL.Path,
				Query:         r.URL.RawQuery,
				Authorization: r.Header.Get("Authorization"),
				WebSocket:     true,
			})
			serveWebSocketEcho(t, w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.record(recordedRequest{
			Path:          r.URL.Path,
			Query:         r.URL.RawQuery,
			Authorization: r.Header.Get("Authorization"),
			ExtraHeaders:  map[string]string{"x-api-key": r.Header.Get("x-api-key")},
			Body:          string(body),
		})

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Mock-Upstream", "ai-deck-converter-e2e")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"mock-chatcmpl","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello from the mock upstream"}}]}`))
	})

	m.Port = serveOnFreePort(t, mux)
	m.URL = fmt.Sprintf("http://host.docker.internal:%d", m.Port)
	return m
}

func (m *mockUpstream) record(req recordedRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
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
	mockExchangedCred  = "upstream-cred-123"
	mockUpstreamSessID = "mock-upstream-session-1"
)

// tokenVaultMocks stands in for the two services the token-vault case needs:
// the Kong Identity vault (identityMock) and the upstream MCP server whose
// tools unlock after enrollment.
type tokenVaultMocks struct {
	IdentityURL  string
	IdentityPort int
	UpstreamURL  string
	UpstreamPort int

	identity *identityMock
}

func startTokenVaultMocks(t *testing.T) *tokenVaultMocks {
	t.Helper()
	identity := startIdentityMock(t)
	identity.Secret = mockExchangedCred
	identity.BearerMethods = []string{"header"}
	port := serveOnFreePort(t, tokenVaultMCPUpstream(t))
	return &tokenVaultMocks{
		IdentityURL:  identity.URL,
		IdentityPort: identity.Port,
		UpstreamURL:  fmt.Sprintf("http://host.docker.internal:%d", port),
		UpstreamPort: port,
		identity:     identity,
	}
}

// tokenVaultMCPUpstream serves a JSON-RPC MCP server whose flights-search
// tool reports the Authorization header it received, so the case can assert
// the exchanged credential was applied upstream.
func tokenVaultMCPUpstream(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		auth := r.Header.Get("Authorization")

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
	return mux
}

func (m *tokenVaultMocks) enroll() {
	m.identity.enroll()
}

func (m *tokenVaultMocks) identityHits() []recordedRequest {
	var out []recordedRequest
	for _, form := range m.identity.calls("/v2/vault/token") {
		out = append(out, recordedRequest{Body: form.Get("subject_token") + "|" + form.Get("resource")})
	}
	return out
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
