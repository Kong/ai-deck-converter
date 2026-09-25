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

// testReusableToolsetsAreInternalOnly drives the aggregate MCP listener that
// references conversion-only sources (team-a, team-b): their tools are
// reachable only through the listener that names them in config.sources. The
// converter gates each conversion-only route with a pre-function that answers
// 404 to anything but ai-mcp-proxy's unix-socket re-entry, so the listener's
// tool calls reach the mock upstream while direct client requests never do,
// even with the listener's own credential.
func testReusableToolsetsAreInternalOnly(t *testing.T, license string) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, "reusable_toolsets_are_internal_only", patchMCPPort(18082, mock.Port))

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
		requireStatus(t, resp, 200, tool+": tools/call")
		if strings.Contains(resp.Body, `"isError":true`) || !strings.Contains(resp.Body, "hello from the mock upstream") {
			t.Fatalf("%s did not execute against the mock upstream\ntools/call response:\n%s", tool, resp.Body)
		}
		t.Logf("PASS: %s executed through the gated conversion-only route", tool)
	}
	var reports int
	for _, hit := range mock.hits() {
		if hit.Path == "/report" {
			reports++
		}
	}
	if reports != 2 {
		t.Fatalf("mock upstream saw %d /report requests, want 2 (one per tool call); hits: %v", reports, mock.hits())
	}

	for _, server := range []string{"team-a", "team-b"} {
		for _, apiKey := range []string{"", mcpE2EAPIKey} {
			context := server + ": direct request without credentials"
			if apiKey != "" {
				context = server + ": direct request with the listener's credential"
			}
			resp := httpPost(t, gateway.ProxyURL()+"/mcp/"+server, mcpHeaders(apiKey, ""),
				`{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}`)
			requireStatus(t, resp, 404, context)
			t.Logf("PASS: %s rejected with 404", context)
		}
	}
}
