//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// aclDeniedBody is the message of a Kong acl plugin rejection. It tells the
// server ACL apart from an ai-mcp-proxy tool ACL rejection.
const aclDeniedBody = "You cannot consume this service"

type mcpProbe struct {
	label  string
	method string
	body   string
}

// mcpProbes has one request for each MCP Streamable HTTP operation.
var mcpProbes = []mcpProbe{
	{"initialize", http.MethodPost, mcpInitializeBody(1)},
	{"notifications/initialized", http.MethodPost, `{"jsonrpc": "2.0", "method": "notifications/initialized"}`},
	{"ping", http.MethodPost, `{"jsonrpc": "2.0", "id": 2, "method": "ping"}`},
	{"tools/list", http.MethodPost, `{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}`},
	{"tools/call", http.MethodPost, toolsCallBody(4, "list-reports")},
	{"resources/list", http.MethodPost, `{"jsonrpc": "2.0", "id": 5, "method": "resources/list"}`},
	{"GET event stream", http.MethodGet, ""},
	{"DELETE session", http.MethodDelete, ""},
}

func toolsCallBody(id int, tool string) string {
	return fmt.Sprintf(`{"jsonrpc": "2.0", "id": %d, "method": "tools/call", "params": {"name": %q, "arguments": {}}}`, id, tool)
}

// requireMCPServerACLDenies sends every MCP operation and requires the acl
// plugin to reject each one with 403. The upstream must see no request.
func requireMCPServerACLDenies(t *testing.T, url string, headers map[string]string, upstream *mockUpstream, who string) {
	t.Helper()
	before := len(upstream.hits())
	for _, probe := range mcpProbes {
		h := make(map[string]string, len(headers)+1)
		for k, v := range headers {
			h[k] = v
		}
		if probe.method != http.MethodPost {
			h["Mcp-Session-Id"] = "e2e-forged-session"
		}
		resp := httpDo(t, probe.method, url, h, probe.body)
		context := fmt.Sprintf("%s: %s", who, probe.label)
		requireStatus(t, resp, 403, context)
		if !strings.Contains(resp.Body, aclDeniedBody) {
			t.Fatalf("%s: got 403 without the acl plugin message %q\nresponse body:\n%s", context, aclDeniedBody, resp.Body)
		}
	}
	if after := upstream.hits(); len(after) != before {
		t.Fatalf("%s: upstream saw %d requests although the server ACL denied every one: %v",
			who, len(after)-before, after[before:])
	}
	t.Logf("PASS: %s: server ACL rejects every MCP operation with 403, upstream untouched", who)
}

// mcpOpenSession starts an MCP session and returns its ID. A stateless server
// gives an empty ID.
func mcpOpenSession(t *testing.T, url string, headers map[string]string, who string) string {
	t.Helper()
	resp := httpPost(t, url, headers, mcpInitializeBody(1))
	requireStatus(t, resp, 200, who+": initialize")
	sess := sessionID(resp)
	h := withSession(headers, sess)
	httpPost(t, url, h, `{"jsonrpc": "2.0", "method": "notifications/initialized"}`)
	return sess
}

func withSession(headers map[string]string, sess string) map[string]string {
	h := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		h[k] = v
	}
	if sess != "" {
		h["Mcp-Session-Id"] = sess
	}
	return h
}

// mcpToolsList returns the tools/list response body.
func mcpToolsList(t *testing.T, url string, headers map[string]string, who string) string {
	t.Helper()
	resp := httpPost(t, url, headers, `{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}`)
	requireStatus(t, resp, 200, who+": tools/list")
	return resp.Body
}

func requireTools(t *testing.T, body, who string, present, absent []string) {
	t.Helper()
	for _, tool := range present {
		if !strings.Contains(body, `"`+tool+`"`) {
			t.Fatalf("%s: tools/list does not have %q\nresponse body:\n%s", who, tool, body)
		}
	}
	for _, tool := range absent {
		if strings.Contains(body, `"`+tool+`"`) {
			t.Fatalf("%s: tools/list has %q, which the tool ACL must hide\nresponse body:\n%s", who, tool, body)
		}
	}
}

func countHits(upstream *mockUpstream, path string) int {
	var n int
	for _, hit := range upstream.hits() {
		if hit.Path == path {
			n++
		}
	}
	return n
}

// testMCPServerACLGatesAllMethods proves that MCP server access.acls is a Kong
// acl plugin in front of ai-mcp-proxy. A denied caller cannot start a session,
// use tools, or open a stream. A caller the server ACL lets in still gets the
// tool ACLs.
func testMCPServerACLGatesAllMethods(t *testing.T, license string) {
	rest := startMockUpstream(t)
	mcp := startMCPUpstream(t)
	configPath := convertCase(t, "mcp_server_acl_gates_all_methods", "2.1",
		patchAll(patchMCPPort(18083, rest.Port), patchMCPPort(18084, mcp.Port)))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.1.0"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	reportsURL := gateway.ProxyURL() + "/mcp/reports"
	vendorURL := gateway.ProxyURL() + "/mcp/vendor"
	key := func(k string) map[string]string { return mcpHeaders(k, "") }

	for _, url := range []string{reportsURL, vendorURL} {
		resp := httpPost(t, url, mcpHeaders("", ""), mcpInitializeBody(1))
		requireStatus(t, resp, 401, url+": request without a key")
		resp = httpPost(t, url, mcpHeaders("not-a-key", ""), mcpInitializeBody(1))
		requireStatus(t, resp, 401, url+": request with an unknown key")
	}
	t.Log("PASS: key-auth rejects a missing or unknown key with 401 on both servers")

	// reports: allow [mcp-allowed].
	requireMCPServerACLDenies(t, reportsURL, key("mcp-outsider-secret"), rest, "reports: consumer in no group")
	requireMCPServerACLDenies(t, reportsURL, key("mcp-buyer-only-secret"), rest,
		"reports: consumer in the tool ACL group buyers only")

	who := "reports: allowed-client"
	h := withSession(key("mcp-allowed-secret"), mcpOpenSession(t, reportsURL, key("mcp-allowed-secret"), who))
	requireTools(t, mcpToolsList(t, reportsURL, h, who), who, []string{"list-reports"}, []string{"buy-report"})
	resp := httpPost(t, reportsURL, h, toolsCallBody(3, "list-reports"))
	requireStatus(t, resp, 200, who+": tools/call list-reports")
	if !strings.Contains(resp.Body, "hello from the mock upstream") || strings.Contains(resp.Body, `"isError":true`) {
		t.Fatalf("%s: list-reports did not run against the mock upstream\nresponse body:\n%s", who, resp.Body)
	}
	resp = httpPost(t, reportsURL, h, toolsCallBody(4, "buy-report"))
	if n := countHits(rest, "/purchases"); n != 0 {
		t.Fatalf("%s: the buyers-only tool reached the upstream (%d hits)\nresponse: HTTP %d\n%s", who, n, resp.Status, resp.Body)
	}
	t.Logf("PASS: %s passes the server ACL, runs list-reports, and the tool ACL keeps buy-report hidden and blocked (HTTP %d)",
		who, resp.Status)

	who = "reports: buyer-client"
	h = withSession(key("mcp-buyer-secret"), mcpOpenSession(t, reportsURL, key("mcp-buyer-secret"), who))
	requireTools(t, mcpToolsList(t, reportsURL, h, who), who, []string{"list-reports", "buy-report"}, nil)
	resp = httpPost(t, reportsURL, h, toolsCallBody(3, "buy-report"))
	requireStatus(t, resp, 200, who+": tools/call buy-report")
	if n := countHits(rest, "/purchases"); n != 1 {
		t.Fatalf("%s: buy-report gave %d upstream hits, want 1\nresponse body:\n%s", who, n, resp.Body)
	}
	t.Logf("PASS: %s passes the server ACL and the buy-report tool ACL", who)

	// A deny on another server does not affect this one.
	who = "reports: blocked-client"
	h = withSession(key("mcp-blocked-secret"), mcpOpenSession(t, reportsURL, key("mcp-blocked-secret"), who))
	requireTools(t, mcpToolsList(t, reportsURL, h, who), who, []string{"list-reports"}, nil)
	t.Logf("PASS: %s, denied only on vendor, still reaches reports", who)

	// vendor: deny [mcp-blocked].
	requireMCPServerACLDenies(t, vendorURL, key("mcp-blocked-secret"), mcp, "vendor: consumer in mcp-blocked")

	for _, k := range []struct{ who, key string }{
		{"vendor: outsider-client", "mcp-outsider-secret"},
		{"vendor: allowed-client", "mcp-allowed-secret"},
	} {
		h := withSession(key(k.key), mcpOpenSession(t, vendorURL, key(k.key), k.who))
		requireTools(t, mcpToolsList(t, vendorURL, h, k.who), k.who, []string{"flights-search"}, nil)
		resp := httpPost(t, vendorURL, h, toolsCallBody(3, "flights-search"))
		requireStatus(t, resp, 200, k.who+": tools/call flights-search")
		if !strings.Contains(resp.Body, "flights-search ok") {
			t.Fatalf("%s: flights-search did not reach the upstream MCP server\nresponse body:\n%s", k.who, resp.Body)
		}
		t.Logf("PASS: %s, not on the deny list, reaches the upstream MCP server", k.who)
	}
}

// testMCPServerACLWhen proves that MCP server allow_when and deny_when
// become CEL conditions on a Kong acl plugin in front of ai-mcp-proxy.
func testMCPServerACLWhen(t *testing.T, license string) {
	rest := startMockUpstream(t)
	mcp := startMCPUpstream(t)
	configPath := convertCase(t, "mcp_server_acl_when", "2.3",
		patchAll(patchMCPPort(18085, rest.Port), patchMCPPort(18086, mcp.Port)))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.3.0"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	flightsURL := gateway.ProxyURL() + "/mcp/flights"
	vendorURL := gateway.ProxyURL() + "/mcp/vendor"
	key := func(k string) map[string]string { return mcpHeaders(k, "") }

	for _, url := range []string{flightsURL, vendorURL} {
		resp := httpPost(t, url, mcpHeaders("", ""), mcpInitializeBody(1))
		requireStatus(t, resp, 401, url+": request without a key")
	}
	t.Log("PASS: key-auth rejects a missing key with 401 before the CEL ACL runs")

	// flights: allow_when the consumer is in the gold group.
	requireMCPServerACLDenies(t, flightsURL, key("mcp-silver-secret"), rest, "flights: consumer in silver")
	requireMCPServerACLDenies(t, flightsURL, key("mcp-no-group-secret"), rest, "flights: consumer in no group")

	for _, c := range []struct{ who, key string }{
		{"flights: gold-client", "mcp-gold-secret"},
		{"flights: suspended-client", "mcp-suspended-secret"},
	} {
		h := withSession(key(c.key), mcpOpenSession(t, flightsURL, key(c.key), c.who))
		requireTools(t, mcpToolsList(t, flightsURL, h, c.who), c.who, []string{"list-reports"}, nil)
		resp := httpPost(t, flightsURL, h, toolsCallBody(3, "list-reports"))
		requireStatus(t, resp, 200, c.who+": tools/call list-reports")
		if !strings.Contains(resp.Body, "hello from the mock upstream") || strings.Contains(resp.Body, `"isError":true`) {
			t.Fatalf("%s: list-reports did not run against the mock upstream\nresponse body:\n%s", c.who, resp.Body)
		}
		t.Logf("PASS: %s matches allow_when and runs list-reports", c.who)
	}

	// vendor: deny_when the consumer is suspended-client.
	requireMCPServerACLDenies(t, vendorURL, key("mcp-suspended-secret"), mcp, "vendor: suspended-client")

	for _, c := range []struct{ who, key string }{
		{"vendor: silver-client", "mcp-silver-secret"},
		{"vendor: no-group-client", "mcp-no-group-secret"},
	} {
		h := withSession(key(c.key), mcpOpenSession(t, vendorURL, key(c.key), c.who))
		requireTools(t, mcpToolsList(t, vendorURL, h, c.who), c.who, []string{"flights-search"}, nil)
		t.Logf("PASS: %s does not match deny_when and reaches the upstream MCP server", c.who)
	}
}

// testMCPOIDCServerACLs proves the 2.3 openid-connect lowering of MCP access.
// Its authenticated groups decide the Kong acl plugin. Its token claims decide
// the oauth_access_token tool ACLs.
func testMCPOIDCServerACLs(t *testing.T, license string) {
	idp := startIDPMock(t)
	mcp := startMCPUpstream(t)
	rest := startMockUpstream(t)
	configPath := convertCase(t, "mcp_oidc_server_acls", "2.3",
		patchAll(patchMCPPort(18087, mcp.Port), patchMCPPort(18088, rest.Port), patchIssuer(18090, idp.Port)))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, "kong/kong-ai-gateway:2.3.0"),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	vendorURL := gateway.ProxyURL() + "/mcp/vendor"
	claimsURL := gateway.ProxyURL() + "/mcp/claims"
	bearer := func(claims map[string]any) map[string]string {
		claims["iss"] = idp.URL
		claims["iat"] = time.Now().Unix()
		claims["exp"] = time.Now().Add(time.Hour).Unix()
		h := mcpHeaders("", "")
		h["Authorization"] = "Bearer " + idp.jwt(t, claims)
		return h
	}

	for _, server := range []string{"vendor", "claims"} {
		status, _, body := httpGet(gateway.ProxyURL() + "/mcp/" + server + "/.well-known/oauth-protected-resource")
		if status != 200 || !strings.Contains(body, "http://localhost/mcp/"+server) {
			t.Fatalf("%s: protected resource metadata: got HTTP %d, want 200 with the resource\n%s", server, status, body)
		}
		resp := httpPost(t, gateway.ProxyURL()+"/mcp/"+server, mcpHeaders("", ""), mcpInitializeBody(1))
		requireStatus(t, resp, 401, server+": request without a bearer token")
		h := mcpHeaders("", "")
		h["Authorization"] = "Bearer not-a-jwt"
		resp = httpPost(t, gateway.ProxyURL()+"/mcp/"+server, h, mcpInitializeBody(1))
		requireStatus(t, resp, 401, server+": request with an invalid bearer token")
	}
	t.Log("PASS: openid-connect serves the metadata and rejects a missing or invalid token with 401 on both servers")

	// vendor: allow [mcp-allowed] against the token's groups claim.
	requireMCPServerACLDenies(t, vendorURL,
		bearer(map[string]any{"sub": "carol", "groups": []string{"other"}}), mcp, "vendor: token with groups [other]")
	requireMCPServerACLDenies(t, vendorURL,
		bearer(map[string]any{"sub": "dave"}), mcp, "vendor: token without a groups claim")

	who := "vendor: token with groups [mcp-allowed]"
	allowed := bearer(map[string]any{"sub": "erin", "groups": []string{"other", "mcp-allowed"}})
	h := withSession(allowed, mcpOpenSession(t, vendorURL, allowed, who))
	requireTools(t, mcpToolsList(t, vendorURL, h, who), who, []string{"flights-search"}, nil)
	resp := httpPost(t, vendorURL, h, toolsCallBody(3, "flights-search"))
	requireStatus(t, resp, 200, who+": tools/call flights-search")
	if !strings.Contains(resp.Body, "flights-search ok") {
		t.Fatalf("%s: flights-search did not reach the upstream MCP server\nresponse body:\n%s", who, resp.Body)
	}
	t.Logf("PASS: %s passes the server ACL and reaches the upstream MCP server", who)

	// claims: default_tool_acls allow [alice@example.com] on the email claim.
	who = "claims: alice@example.com"
	alice := bearer(map[string]any{"sub": "alice", "email": "alice@example.com"})
	h = withSession(alice, mcpOpenSession(t, claimsURL, alice, who))
	requireTools(t, mcpToolsList(t, claimsURL, h, who), who, []string{"flights-search"}, nil)
	resp = httpPost(t, claimsURL, h, toolsCallBody(3, "flights-search"))
	requireStatus(t, resp, 200, who+": tools/call flights-search")
	if !strings.Contains(resp.Body, "flights-search ok") {
		t.Fatalf("%s: flights-search did not reach the upstream MCP server\nresponse body:\n%s", who, resp.Body)
	}
	t.Logf("PASS: %s matches the oauth_access_token tool ACL and calls flights-search", who)

	who = "claims: bob@example.com"
	bob := bearer(map[string]any{"sub": "bob", "email": "bob@example.com"})
	h = withSession(bob, mcpOpenSession(t, claimsURL, bob, who))
	requireTools(t, mcpToolsList(t, claimsURL, h, who), who, nil, []string{"flights-search"})
	before := countToolCalls(mcp)
	resp = httpPost(t, claimsURL, h, toolsCallBody(3, "flights-search"))
	if strings.Contains(resp.Body, "flights-search ok") || countToolCalls(mcp) != before {
		t.Fatalf("%s: flights-search reached the upstream although the tool ACL denies it\nresponse: HTTP %d\n%s",
			who, resp.Status, resp.Body)
	}
	t.Logf("PASS: %s does not match the tool ACL: flights-search is hidden and blocked (HTTP %d)", who, resp.Status)

	// reports: a conversion-listener tool call passes openid-connect twice.
	reportsURL := gateway.ProxyURL() + "/mcp/reports"
	resp = httpPost(t, reportsURL, mcpHeaders("", ""), mcpInitializeBody(1))
	requireStatus(t, resp, 401, "reports: request without a bearer token")
	who = "reports: valid token"
	token := bearer(map[string]any{"sub": "frank"})
	h = withSession(token, mcpOpenSession(t, reportsURL, token, who))
	requireTools(t, mcpToolsList(t, reportsURL, h, who), who, []string{"list-reports"}, nil)
	resp = httpPost(t, reportsURL, h, toolsCallBody(3, "list-reports"))
	requireStatus(t, resp, 200, who+": tools/call list-reports")
	if !strings.Contains(resp.Body, "hello from the mock upstream") || strings.Contains(resp.Body, `"isError":true`) {
		t.Fatalf("%s: list-reports did not run against the mock upstream\nresponse body:\n%s", who, resp.Body)
	}
	t.Logf("PASS: %s runs a conversion-listener tool through the openid-connect route", who)
}

func countToolCalls(upstream *mockUpstream) int {
	var n int
	for _, hit := range upstream.hits() {
		if strings.Contains(hit.Body, `"tools/call"`) {
			n++
		}
	}
	return n
}
