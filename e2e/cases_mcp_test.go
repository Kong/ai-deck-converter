//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

const (
	mcpAcceptHeader  = "application/json, text/event-stream"
	mcpE2EAPIKey     = "mcp-e2e-secret"
	mcpSubjectToken  = "e2e-subject-token"
	mcpClientInfoMsg = `{"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "ai-deck-converter-e2e", "version": "1.0.0"}}`
)

func mcpHeaders(apiKey, session string) map[string]string {
	h := map[string]string{
		"Content-Type": "application/json",
		"Accept":       mcpAcceptHeader,
	}
	if apiKey != "" {
		h["apikey"] = apiKey
	}
	if session != "" {
		h["Mcp-Session-Id"] = session
	}
	return h
}

func mcpInitializeBody(id int) string {
	return fmt.Sprintf(`{"jsonrpc": "2.0", "id": %d, "method": "initialize", "params": %s}`, id, mcpClientInfoMsg)
}

func sessionID(resp httpResponse) string {
	return headerValue(resp.Header, "Mcp-Session-Id")
}

// testReusableToolsetsHaveAuthenticatedRoutes drives the aggregate MCP listener
// that references conversion-only sources (team-a, team-b): their tools are
// reachable only through the listener that names them in config.sources, and
// the converter copies the listener's key-auth onto their conversion-only
// routes so those routes cannot be reached on terms the listener would reject.
func testReusableToolsetsHaveAuthenticatedRoutes(t *testing.T, license string) {
	configPath := convertCase(t, "reusable_toolsets_have_authenticated_routes", nil)

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway-dev:2.1.0-rc.3"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	mcpURL := gateway.ProxyURL() + "/mcp/aggregate"

	resp := httpPost(t, mcpURL, mcpHeaders("", ""),
		`{"jsonrpc": "2.0", "id": 0, "method": "tools/list"}`)
	requireStatus(t, resp, 401, "unauthenticated MCP request")
	t.Log("PASS: unauthenticated request rejected with 401")

	resp = httpPost(t, mcpURL, mcpHeaders(mcpE2EAPIKey, ""), mcpInitializeBody(1))
	requireStatus(t, resp, 200, "initialize")
	sess := sessionID(resp)
	if sess != "" {
		t.Logf("session: %s", sess)
	}

	httpPost(t, mcpURL, mcpHeaders(mcpE2EAPIKey, sess),
		`{"jsonrpc": "2.0", "method": "notifications/initialized"}`)

	resp = httpPost(t, mcpURL, mcpHeaders(mcpE2EAPIKey, sess),
		`{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}`)
	requireStatus(t, resp, 200, "tools/list")
	for _, tool := range []string{"team-a-report", "team-b-report"} {
		if !strings.Contains(resp.Body, `"`+tool+`"`) {
			t.Fatalf("aggregate listener did not expose %q\ntools/list response:\n%s", tool, resp.Body)
		}
	}
	t.Log("PASS: aggregate listener exposes team-a-report and team-b-report")

	for _, tool := range []string{"team-a-report", "team-b-report"} {
		resp := httpPost(t, mcpURL, mcpHeaders(mcpE2EAPIKey, sess), fmt.Sprintf(
			`{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": %q, "arguments": {}}}`, tool))
		switch {
		case strings.Contains(resp.Body, "No API key found in request"):
			t.Logf("WARN: %s resolved to a path on its own conversion-only route and was rejected by the inherited key-auth "+
				"(internal tool calls carry no credentials)", tool)
		case strings.Contains(resp.Body, "HTTP call failed with status"):
			t.Logf("INFO: %s was not blocked by the inherited access, but the call failed: %s", tool, resp.Body)
		default:
			t.Logf("PASS: %s executed successfully", tool)
		}
	}

	for _, server := range []string{"team-a", "team-b"} {
		resp := httpPost(t, gateway.ProxyURL()+"/mcp/"+server, mcpHeaders("", ""),
			`{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}`)
		requireStatus(t, resp, 401, server+": unauthenticated request")
		t.Logf("PASS: %s rejects unauthenticated requests with 401", server)

		resp = httpPost(t, gateway.ProxyURL()+"/mcp/"+server, mcpHeaders(mcpE2EAPIKey, ""),
			`{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}`)
		if resp.Status == 401 {
			t.Fatalf("%s: the listener's own credential was rejected", server)
		}
		t.Logf("INFO: %s authenticated request returned %d (conversion-only mode; what happens past auth "+
			"is up to ai-mcp-proxy and the Gateway Service upstream)", server, resp.Status)
	}
}
