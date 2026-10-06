//go:build e2e

package e2e

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const (
	realtimePremiumKey = "premium-secret"
	realtimeBasicKey   = "basic-secret"
)

// testRealtimeModelWebSocketTransport proves a model with chat and realtime
// capabilities serves both transports. The chat route stays on the HTTP
// Service. The realtime route is on the ws Service, and ws and wss clients
// reach the mock upstream through it with the provider credential applied.
// Access control holds on both transports. key-auth on the realtime route has
// no anonymous fallback, because request-termination does not run on ws and wss.
func testRealtimeModelWebSocketTransport(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "realtime_model_websocket_transport", patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:    gatewayImage(t, "kong/kong-ai-gateway:2.0.2"),
		Config:   configPath,
		Env:      []string{licenseEnv(license)},
		ProxyTLS: true,
	})
	gateway.waitReady()

	t.Run("routes loaded on their transport", func(t *testing.T) {
		requireRouteProtocols(t, gateway, "openai-chat", []string{"http", "https"})
		requireRouteProtocols(t, gateway, "openai-realtime", []string{"ws", "wss"})
	})

	t.Run("chat over http", func(t *testing.T) {
		chat := func(key, content string) httpResponse {
			headers := map[string]string{"Content-Type": "application/json"}
			if key != "" {
				headers["apikey"] = key
			}
			return httpPost(t, gateway.ProxyURL()+"/ai-realtime/chat/completions", headers,
				`{"model": "gpt-realtime-mixed", "messages": [{"role": "user", "content": "`+content+`"}]}`)
		}

		resp := chat(realtimePremiumKey, "Say hello.")
		requireStatus(t, resp, 200, "chat completion with the premium key")
		if !strings.Contains(resp.Body, "hello from the mock upstream") {
			t.Fatalf("chat response did not come from the mock upstream:\n%s", resp.Body)
		}
		// key-auth falls back to the anonymous consumer on HTTP. acl runs before
		// request-termination and rejects that consumer with 403.
		requireStatus(t, chat("", "Say hello."), 403, "chat completion without a key")
		requireStatus(t, chat(realtimeBasicKey, "Say hello."), 403, "chat completion outside the ACL group")
		requireStatus(t, chat(realtimePremiumKey, "Say something forbidden."), 400, "chat completion the prompt guard denies")
	})

	tlsURL, err := url.Parse(gateway.ProxyTLSURL())
	if err != nil {
		t.Fatalf("parsing the TLS proxy URL: %v", err)
	}
	plainURL, err := url.Parse(gateway.ProxyURL())
	if err != nil {
		t.Fatalf("parsing the proxy URL: %v", err)
	}
	for _, endpoint := range []string{
		"wss://" + tlsURL.Host + "/ai-realtime/realtime",
		"ws://" + plainURL.Host + "/ai-realtime/realtime",
	} {
		scheme := strings.SplitN(endpoint, ":", 2)[0]
		target := endpoint + "?model=gpt-realtime-mixed"

		t.Run(scheme+" rejects a missing key", func(t *testing.T) {
			requireUpgradeRejected(t, mock, target, nil, 401)
		})
		t.Run(scheme+" rejects a consumer outside the ACL group", func(t *testing.T) {
			requireUpgradeRejected(t, mock, target, map[string]string{"apikey": realtimeBasicKey}, 403)
		})
		t.Run(scheme+" proxies a realtime session", func(t *testing.T) {
			before := len(mock.hits())
			conn, status, body := dialWebSocket(t, target, map[string]string{"apikey": realtimePremiumKey})
			if conn == nil {
				t.Fatalf("WebSocket upgrade to %s returned HTTP %d, want 101:\n%s", endpoint, status, body)
			}
			defer conn.close()

			const event = `{"type":"session.update","session":{"instructions":"Say hello."}}`
			conn.sendText(t, event)
			reply := conn.receiveText(t)
			var echo struct {
				Type  string          `json:"type"`
				Event json.RawMessage `json:"event"`
			}
			if err := json.Unmarshal([]byte(reply), &echo); err != nil || echo.Type != "mock.echo" ||
				!jsonEqual(string(echo.Event), event) {
				t.Fatalf("frame did not round-trip through the mock upstream: got %q", reply)
			}

			hits := mock.hits()[before:]
			if len(hits) != 1 || !hits[0].WebSocket {
				t.Fatalf("expected one WebSocket upgrade at the mock upstream, saw %+v", hits)
			}
			if hits[0].Authorization != mockAuthHeaderValue {
				t.Fatalf("upstream handshake carried Authorization %q, want %q",
					hits[0].Authorization, mockAuthHeaderValue)
			}
			// AI Gateway 2.0.2 sends the client model on the first realtime
			// session of each nginx worker. Later sessions get the target model.
			query, _ := url.ParseQuery(hits[0].Query)
			if got := query.Get("model"); got != "gpt-realtime" && got != "gpt-realtime-mixed" {
				t.Fatalf("upstream handshake carried model %q, want gpt-realtime or gpt-realtime-mixed", got)
			}
			t.Logf("PASS: %s proxied a realtime frame to the mock upstream", endpoint)
		})
	}
}

// requireUpgradeRejected asserts the gateway refuses the WebSocket handshake
// with status and the request never reaches the mock upstream.
func requireUpgradeRejected(t *testing.T, mock *mockUpstream, target string, headers map[string]string, status int) {
	t.Helper()
	before := len(mock.hits())
	conn, got, body := dialWebSocket(t, target, headers)
	if conn != nil {
		conn.close()
		t.Fatalf("WebSocket upgrade to %s succeeded, want HTTP %d", target, status)
	}
	if got != status {
		t.Fatalf("WebSocket upgrade to %s returned HTTP %d, want %d:\n%s", target, got, status, body)
	}
	if hits := mock.hits()[before:]; len(hits) != 0 {
		t.Fatalf("a rejected upgrade reached the mock upstream: %+v", hits)
	}
}

func requireRouteProtocols(t *testing.T, gateway *Gateway, name string, want []string) {
	t.Helper()
	status, _, body := httpGet(gateway.AdminURL() + "/routes/" + name)
	if status != 200 {
		t.Fatalf("GET /routes/%s returned %d:\n%s", name, status, body)
	}
	var route struct {
		Protocols []string `json:"protocols"`
	}
	if err := json.Unmarshal([]byte(body), &route); err != nil {
		t.Fatalf("decoding GET /routes/%s: %v\n%s", name, err, body)
	}
	if !slices.Equal(route.Protocols, want) {
		t.Fatalf("route %q has protocols %v, want %v", name, route.Protocols, want)
	}
}

func jsonEqual(a, b string) bool {
	var va, vb any
	if json.Unmarshal([]byte(a), &va) != nil || json.Unmarshal([]byte(b), &vb) != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}
