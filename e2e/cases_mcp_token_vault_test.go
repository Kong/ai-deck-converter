//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// testTokenVaultGatesToolsAndEnrolls walks the Token Vault lifecycle: an
// unenrolled caller is gated to the virtual enrollment tools, the mock Kong
// Identity service answers the exchange with x_kong_enrollment_required until
// enrollment, and afterwards the exchanged credential (shared through Redis,
// encrypted) is applied to proxied upstream MCP calls.
func testTokenVaultGatesToolsAndEnrolls(t *testing.T, license string) {
	d := dockerCLI{t: t}
	runID := fmt.Sprintf("%d", time.Now().UnixNano()%1e6)

	mocks := startTokenVaultMocks(t)
	configPath := convertCase(t, "token_vault_gates_tools_and_enrolls", patchMCPPort(18081, mocks.UpstreamPort))

	network := "aidc-e2e-tv-" + runID
	redisName := "aidc-e2e-tv-redis-" + runID
	d.run("network", "create", network)
	t.Cleanup(func() {
		d.remove(redisName)
		_, _ = execCommand("docker", "network", "rm", network)
	})
	d.run("run", "-d", "--name", redisName, "--network", network, "redis:7-alpine")
	redisIP := d.run("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", redisName)

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway-dev:35727837758-070f4458537dcb4fe4d6c7321447cf902d2e07c9"),
		Config: configPath,
		Env: []string{
			licenseEnv(license),
			"KONG_IDENTITY_SERVICE=" + mocks.IdentityURL,
		},
		RunArgs: []string{
			"--network", network,
			"--add-host", "mcp-tv-redis:" + redisIP,
		},
	})
	gateway.waitReady()

	mcpURL := gateway.ProxyURL() + "/mcp/vaulted"
	request := func(label, body, session string) httpResponse {
		t.Helper()
		headers := mcpHeaders("", session)
		headers["Authorization"] = "Bearer " + mcpSubjectToken
		resp := httpPost(t, mcpURL, headers, body)
		if label != "" {
			t.Logf("--> %s -> HTTP %d", label, resp.Status)
		}
		return resp
	}

	resp := httpPost(t, mcpURL, mcpHeaders("", ""),
		`{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "e2e", "version": "1.0"}}}`)
	if resp.Status != 500 {
		t.Fatalf("request without a subject token returned %d, expected 500 (the plugin cannot resolve the upstream credential)\n%s", resp.Status, resp.Body)
	}
	t.Log("PASS: no subject token -> upstream auth failure (token_vault is active)")

	resp = request("initialize (gated)", mcpInitializeBody(1), "")
	requireStatus(t, resp, 200, "gated initialize")
	if !strings.Contains(resp.Body, `"result"`) {
		t.Fatalf("gated initialize did not succeed:\n%s\nidentity mock requests: %v", resp.Body, mocks.identityHits())
	}
	sess := sessionID(resp)
	t.Logf("session: %s", sess)

	request("notifications/initialized", `{"jsonrpc": "2.0", "method": "notifications/initialized"}`, sess)

	resp = request("tools/list (gated)", `{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}`, sess)
	for _, tool := range []string{"token_vault_authenticate", "token_vault_check_authentication_status"} {
		if !strings.Contains(resp.Body, `"`+tool+`"`) {
			t.Fatalf("gated tools/list did not offer virtual tool %s\ntools/list response:\n%s", tool, resp.Body)
		}
	}
	if strings.Contains(resp.Body, "flights-search") {
		t.Fatalf("gated tools/list leaked the real upstream tools:\n%s", resp.Body)
	}
	t.Log("PASS: gated caller sees only the virtual enrollment tools")

	resp = request("tools/call token_vault_authenticate",
		`{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "token_vault_authenticate", "arguments": {}}}`, sess)
	if !strings.Contains(resp.Body, mockEnrollmentURL) {
		t.Fatalf("authenticate did not return the enrollment URL (%s):\n%s", mockEnrollmentURL, resp.Body)
	}
	t.Log("PASS: authenticate returned the enrollment URL")

	mocks.enroll()

	resp = request("tools/call token_vault_check_authentication_status",
		`{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "token_vault_check_authentication_status", "arguments": {}}}`, sess)
	if !strings.Contains(resp.Body, "Authorization complete") {
		t.Fatalf("check_authentication_status did not confirm enrollment:\n%s", resp.Body)
	}
	t.Log("PASS: check_authentication_status confirmed enrollment")

	scan := d.run("exec", redisName, "redis-cli", "--scan", "--pattern", "ai-mcp-proxy:vault:*")
	if scan == "" {
		t.Fatalf("no exchanged credential found in the token_vault Redis cache\n%s", gateway.logs(200))
	}
	t.Log("PASS: exchanged credential cached in the token_vault Redis (L2)")

	resp = request("initialize (enrolled)", mcpInitializeBody(5), "")
	requireStatus(t, resp, 200, "enrolled initialize")
	upstreamSess := sessionID(resp)
	t.Logf("upstream session: %s", upstreamSess)

	resp = request("tools/list (enrolled)", `{"jsonrpc": "2.0", "id": 6, "method": "tools/list"}`, upstreamSess)
	if !strings.Contains(resp.Body, "flights-search") {
		t.Fatalf("enrolled tools/list did not expose the real upstream tools:\n%s", resp.Body)
	}
	t.Log("PASS: enrolled caller sees the real upstream tools")

	resp = request("tools/call flights-search",
		`{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {"name": "flights-search", "arguments": {}}}`, upstreamSess)
	if !strings.Contains(resp.Body, "upstream saw Authorization: Bearer "+mockExchangedCred) {
		t.Fatalf("the upstream did not receive the exchanged credential:\n%s", resp.Body)
	}
	t.Log("PASS: upstream received the exchanged credential")
}
