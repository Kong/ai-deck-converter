//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testTokenVaultExchangeCredentialPlacement drives token-vault-exchange on
// two model routes: one provider pins every credential lever (x-api-key
// placements with no prefix, subject_token_type api_key), one omits them all
// so the plugin defaults and the vault's bearer-method advertisement decide.
// It also proves the /_vault callback route is mounted.
func testTokenVaultExchangeCredentialPlacement(t *testing.T, license string) {
	upstream := startMockUpstream(t)
	identity := startIdentityMock(t)
	identity.Secret = "upstream-secret-abc"

	configPath := convertCase(t, "token_vault_exchange_credential_placement", "2.3",
		patchVaultedTargets(upstream.Port))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, tokenVaultImage),
		Config: configPath,
		Env: []string{
			licenseEnv(license),
			"KONG_IDENTITY_SERVICE=" + identity.URL,
		},
	})
	gateway.waitReady()
	proxy := gateway.ProxyURL()

	// The callback route is mounted and its plugin answers, not Kong's
	// no-route JSON.
	resp := httpGetNoRedirect(t, &http.Client{Timeout: 30 * time.Second},
		proxy+"/_vault/does-not-exist", nil)
	if resp.Status != 404 || resp.Body != "Not found\n" {
		t.Fatalf("GET /_vault/does-not-exist: got HTTP %d %q, want 404 %q\n%s",
			resp.Status, resp.Body, "Not found\n", gateway.logs(200))
	}
	t.Log("PASS: /_vault is mounted and served by token-vault-callback-handler")

	post := func(path, model string, headers map[string]string) httpResponse {
		t.Helper()
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Content-Type"] = "application/json"
		body := `{"model": "` + model + `", "max_tokens": 10, "messages": [{"role": "user", "content": "hi"}]}`
		resp := httpPost(t, proxy+path, headers, body)
		t.Logf("--> %s %s -> HTTP %d", path, model, resp.Status)
		return resp
	}

	// The inbound placement is x-api-key: no credential, and a bare
	// Authorization header, both fail before the vault is called.
	resp = post("/explicit/v1/messages", "claude", nil)
	if resp.Status != 401 {
		t.Fatalf("no-credential request: got HTTP %d, want 401\n%s", resp.Status, resp.Body)
	}
	resp = post("/explicit/v1/messages", "claude", map[string]string{
		"Authorization": "Bearer wrong-place",
	})
	if resp.Status != 401 {
		t.Fatalf("Authorization-only request: got HTTP %d, want 401\n%s", resp.Status, resp.Body)
	}
	if got := len(identity.calls("/v2/vault/token")); got != 0 {
		t.Fatalf("the vault was called %d time(s) without an x-api-key, want 0", got)
	}
	t.Log("PASS: inbound placement is x-api-key, not the default Authorization header")

	// Enrollment-required carries the rehosted URL, and the exchange form
	// proves prefix: "" and subject_token_type reached the plugin.
	resp = post("/explicit/v1/messages", "claude", map[string]string{
		"x-api-key": "raw-subject-key",
	})
	if resp.Status != 401 {
		t.Fatalf("unenrolled request: got HTTP %d, want 401\n%s", resp.Status, resp.Body)
	}
	if !strings.Contains(resp.Body, "enrollment_required") ||
		!strings.Contains(resp.Body, "/_vault/callback/initiate?token=") {
		t.Fatalf("unenrolled response missing the rehosted enrollment URL:\n%s", resp.Body)
	}
	forms := identity.calls("/v2/vault/token")
	if len(forms) == 0 {
		t.Fatalf("no exchange call recorded\n%s", gateway.logs(200))
	}
	form := forms[len(forms)-1]
	if form.Get("subject_token") != "raw-subject-key" {
		t.Fatalf("subject_token = %q, want the raw value (prefix \"\" suppressed \"Bearer \")", form.Get("subject_token"))
	}
	if form.Get("subject_token_type") != "urn:konghq.com:params:oauth:token-type:api_key" {
		t.Fatalf("subject_token_type = %q, want the api_key URN", form.Get("subject_token_type"))
	}
	if form.Get("resource") != "anthropic" {
		t.Fatalf("resource = %q, want anthropic", form.Get("resource"))
	}
	t.Log("PASS: inbound prefix and subject_token_type reached the data plane")

	// The explicit outbound placement injects x-api-key and strips
	// Authorization. The negative cache holds 2s, so poll past it.
	identity.enroll()
	var ok bool
	for i := 0; i < 10 && !ok; i++ {
		resp = post("/explicit/v1/messages", "claude", map[string]string{
			"x-api-key": "raw-subject-key",
		})
		ok = resp.Status == 200
		if !ok {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if !ok {
		t.Fatalf("enrolled request: got HTTP %d, want 200\n%s\n%s", resp.Status, resp.Body, gateway.logs(200))
	}
	var hit recordedRequest
	for _, h := range upstream.hits() {
		if h.ExtraHeaders["x-api-key"] != "" {
			hit = h
		}
	}
	if hit.Path == "" {
		t.Fatalf("no upstream hit carried x-api-key\n%s", gateway.logs(200))
	}
	if hit.ExtraHeaders["x-api-key"] != "upstream-secret-abc" {
		t.Fatalf("outbound x-api-key = %q, want upstream-secret-abc", hit.ExtraHeaders["x-api-key"])
	}
	if hit.Authorization != "" {
		t.Fatalf("Authorization = %q upstream, want it stripped", hit.Authorization)
	}
	t.Log("PASS: explicit outbound placement injected the exchanged credential")

	// Defaults leg: the omitted inbound strips "Bearer ", the omitted
	// subject_token_type is shape-inferred, and the omitted outbound follows
	// the x-kong-bearer-methods-supported advertisement.
	identity.Secret = "upstream-secret-def"
	identity.BearerMethods = []string{"header"}
	resp = post("/defaults/chat/completions", "gpt", map[string]string{
		"Authorization": "Bearer e2e-jwt-less-token",
	})
	if resp.Status != 200 {
		t.Fatalf("defaulted request: got HTTP %d, want 200\n%s\n%s", resp.Status, resp.Body, gateway.logs(200))
	}
	var gptForm url.Values
	for _, f := range identity.calls("/v2/vault/token") {
		if f.Get("resource") == "openai" {
			gptForm = f
		}
	}
	if gptForm == nil {
		t.Fatalf("no exchange call for the openai provider\n%s", gateway.logs(200))
	}
	if gptForm.Get("subject_token") != "e2e-jwt-less-token" {
		t.Fatalf("subject_token = %q, want the default inbound prefix \"Bearer \" stripped", gptForm.Get("subject_token"))
	}
	if gptForm.Get("subject_token_type") != "urn:konghq.com:params:oauth:token-type:api_key" {
		t.Fatalf("subject_token_type = %q, want the runtime-inferred api_key URN", gptForm.Get("subject_token_type"))
	}
	var gptHit recordedRequest
	for _, h := range upstream.hits() {
		if h.Authorization != "" && h.ExtraHeaders["x-api-key"] == "" {
			gptHit = h
		}
	}
	if gptHit.Authorization != "Bearer upstream-secret-def" {
		t.Fatalf("upstream Authorization = %q, want the advertised header placement", gptHit.Authorization)
	}
	t.Log("PASS: omitted credential config resolved its defaults at runtime")
}

// testTokenVaultCallbackOIDCEnrollment walks the full browser enrollment
// flow: the openid-connect guard on /_vault logs the caller in, the
// callback handler binds the enrollment to the OIDC access token, and the
// enrollment code is redeemed by the callback handler, not by openid-connect.
func testTokenVaultCallbackOIDCEnrollment(t *testing.T, license string) {
	idp := startIDPMock(t)
	identity := startIdentityMock(t)
	identity.AuthURLBase = idp.URL
	identity.TokenEndpoint = idp.URL + "/token"

	configPath := convertCase(t, "token_vault_callback_oidc_enrollment", "2.3",
		patchIssuer(18090, idp.Port))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, tokenVaultImage),
		Config: configPath,
		Env: []string{
			licenseEnv(license),
			"KONG_IDENTITY_SERVICE=" + identity.URL,
		},
	})
	gateway.waitReady()
	proxy := gateway.ProxyURL()
	idp.setGatewayOrigin(proxy)

	client, jar := newBrowserClient(t, proxy)
	hop := func(u string) httpResponse {
		t.Helper()
		return httpGetNoRedirect(t, client, u, nil)
	}
	initiate := proxy + "/_vault/callback/initiate?token=" + mockEnrollmentBlob

	// The completion path carries no openid-connect plugin. The callback
	// handler answers the request itself, so no login can discard the
	// single-use code of the upstream provider.
	bare := httpGetNoRedirect(t, &http.Client{Timeout: 30 * time.Second},
		proxy+"/_vault/callback?code=x&state=y", nil)
	if bare.Status != 200 || bare.Location() != "" ||
		!strings.Contains(bare.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("sessionless GET /_vault/callback: got HTTP %d, Location %q, %q\n%s",
			bare.Status, bare.Location(), bare.Header.Get("Content-Type"), gateway.logs(200))
	}
	t.Log("PASS: /_vault/callback runs no login; the handler answers the forged state itself")

	// The guard runs the authorization_code login: 302 to the IdP, with the
	// authorization cookie scoped to /_vault.
	r1 := hop(initiate)
	if r1.Status != 302 {
		t.Fatalf("initiate: got HTTP %d, want 302\n%s\n%s", r1.Status, r1.Body, gateway.logs(200))
	}
	r1URL, err := url.Parse(r1.Location())
	if err != nil || r1URL.Host != idpURLHost(idp) {
		t.Fatalf("initiate redirected to %q, want the IdP", r1.Location())
	}
	if auth := setCookie(r1, "authorization"); !strings.Contains(auth, "Path=/_vault/callback/initiate") {
		t.Fatalf("authorization cookie = %q, want Path=/_vault/callback/initiate", auth)
	}
	t.Log("PASS: openid-connect guard started the login with the initiate authorization cookie path")

	// IdP /authorize redirects back; the guard completes the code exchange
	// and redirects to the original URL itself, so the session cookie lands
	// on the plugin's own response instead of riding the callback handler's
	// 302 (which would replace it).
	r2 := hop(r1.Location())
	if r2.Status != 302 {
		t.Fatalf("IdP /authorize: got HTTP %d, want 302\n%s", r2.Status, r2.Body)
	}
	r3 := hop(r2.Location())
	r3URL, r3Err := url.Parse(r3.Location())
	if r3.Status != 302 || r3Err != nil || r3URL.Path != "/_vault/callback/initiate" ||
		r3URL.Query().Get("token") != mockEnrollmentBlob {
		t.Fatalf("login redirect: got HTTP %d -> %q, want 302 -> the initiate URL\n%s\n%s",
			r3.Status, r3.Location(), r3.Body, gateway.logs(200))
	}
	if session := setCookie(r3, "session"); session == "" {
		t.Fatalf("the login redirect carries no session cookie:\n%q", r3.Header.Values("Set-Cookie"))
	}
	t.Log("PASS: login_action redirect preserved the session cookie")

	// The replayed initiate runs with the session: the callback handler
	// receives the query args and the OIDC access token, and 302s to the
	// Identity authorization URL.
	r4 := hop(r3.Location())
	if r4.Status != 302 {
		t.Fatalf("post-login initiate: got HTTP %d, want 302\n%s\n%s", r4.Status, r4.Body, gateway.logs(200))
	}
	wantAuthURL := idp.URL + "/vault-authorize?state=" + url.QueryEscape(mockStateToken())
	if r4.Location() != wantAuthURL {
		t.Fatalf("post-login redirect = %q, want %q", r4.Location(), wantAuthURL)
	}
	if state := setCookie(r4, "kong_vault_state"); !strings.Contains(state, "Path=/_vault/callback") {
		t.Fatalf("kong_vault_state cookie = %q, want Path=/_vault/callback", state)
	}
	resolve := identity.calls("/v2/vault/enrollment/resolve")
	if len(resolve) != 1 {
		t.Fatalf("identity mock saw %d enrollment/resolve calls, want 1", len(resolve))
	}
	if got := resolve[0].Get("token"); got != mockEnrollmentBlob {
		t.Fatalf("resolve token = %q, want the enrollment blob (preserve_query_args)", got)
	}
	tokenCalls := idp.tokenCalls()
	if len(tokenCalls) != 1 {
		t.Fatalf("idp mock saw %d /token calls before the vault callback, want 1 (the login)", len(tokenCalls))
	}
	if got := resolve[0].Get("subject_token"); got != tokenCalls[0].AccessToken {
		t.Fatalf("resolve subject_token = %q, want the OIDC access token %q (hide_credentials false)", got, tokenCalls[0].AccessToken)
	}
	if got := resolve[0].Get("subject_token_type"); got != "urn:ietf:params:oauth:token-type:access_token" {
		t.Fatalf("resolve subject_token_type = %q, want the access_token URN", got)
	}
	if got := resolve[0].Get("redirect_uri"); !strings.HasSuffix(got, "/_vault/callback") {
		t.Fatalf("resolve redirect_uri = %q, want it to end with /_vault/callback", got)
	}
	t.Log("PASS: the callback handler received the query args and the OIDC access token")

	// The IdP's vault-authorize redirects to the gateway callback. The
	// session already exists, so openid-connect does not touch the code:
	// the callback handler runs its own exchange and completes enrollment.
	r5 := hop(r4.Location())
	if r5.Status != 302 {
		t.Fatalf("IdP /vault-authorize: got HTTP %d, want 302\n%s", r5.Status, r5.Body)
	}
	r6 := hop(r5.Location())
	if r6.Status != 200 || !strings.Contains(r6.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("vault callback: got HTTP %d %q, want 200 text/html\n%s\n%s",
			r6.Status, r6.Header.Get("Content-Type"), r6.Body, gateway.logs(200))
	}
	if !strings.Contains(r6.Body, "Authorization complete") {
		t.Fatalf("vault callback page does not confirm the enrollment:\n%s", r6.Body)
	}

	resolveState := identity.calls("/v2/vault/enrollment/resolve-state")
	if len(resolveState) != 1 || resolveState[0].Get("state_token") != mockStateToken() {
		t.Fatalf("identity mock saw %d unexpected resolve-state calls", len(resolveState))
	}
	complete := identity.calls("/v2/vault/enrollment/complete")
	if len(complete) != 1 {
		t.Fatalf("identity mock saw %d enrollment/complete calls, want 1", len(complete))
	}
	var envelope struct {
		V    any              `json:"v"`
		Deks []map[string]any `json:"deks"`
		IV   string           `json:"iv"`
		CT   string           `json:"ct"`
		Tag  string           `json:"tag"`
	}
	if err := json.Unmarshal([]byte(complete[0].Get("credential_reference")), &envelope); err != nil ||
		envelope.V == nil || len(envelope.Deks) == 0 || envelope.IV == "" || envelope.CT == "" || envelope.Tag == "" {
		t.Fatalf("credential_reference is not a sealed envelope: %v (%q)",
			err, complete[0].Get("credential_reference"))
	}
	t.Log("PASS: the gateway sealed the credential with the datastore storage key")

	tokenCalls = idp.tokenCalls()
	if len(tokenCalls) != 2 {
		t.Fatalf("idp mock saw %d /token calls, want 2", len(tokenCalls))
	}
	if got := tokenCalls[1].Form.Get("grant_type"); got != "authorization_code" {
		t.Fatalf("second /token grant_type = %q, want authorization_code", got)
	}
	if got := tokenCalls[1].Form.Get("redirect_uri"); !strings.HasSuffix(got, "/_vault/callback") {
		t.Fatalf("second /token redirect_uri = %q, want it to end with /_vault/callback", got)
	}
	t.Log("PASS: the enrollment code was redeemed by the callback handler, not openid-connect")

	// CSRF control: a caller with a session but no state cookie reaches the
	// callback handler, which rejects the forged callback.
	freshClient, freshJar := newBrowserClient(t, proxy)
	proxyURL, _ := url.Parse(proxy)
	for _, c := range jar.Cookies(proxyURL) {
		if c.Name != "kong_vault_state" {
			freshJar.SetCookies(proxyURL, []*http.Cookie{c})
		}
	}
	forged := httpGetNoRedirect(t, freshClient, proxy+"/_vault/callback?code=x&state=y", nil)
	if forged.Status != 200 || !strings.Contains(forged.Body, "Invalid") || strings.Contains(forged.Body, "Authorization complete") {
		t.Fatalf("forged callback: got HTTP %d, body:\n%s", forged.Status, forged.Body)
	}
	t.Log("PASS: the forged callback was rejected by the state-cookie binding")
}

// testTokenVaultCredentialManager drives the connectors UI that
// enable_credential_manager mounts: the page route falls back to the
// anonymous consumer, so the credential manager itself serves the login
// page and rejects anonymous data requests. The login route runs the
// browser login, and /providers aggregates the Identity provider list with
// a live enrollment probe per provider.
func testTokenVaultCredentialManager(t *testing.T, license string) {
	idp := startIDPMock(t)
	identity := startIdentityMock(t)
	identity.AuthURLBase = idp.URL
	identity.TokenEndpoint = idp.URL + "/token"
	identity.Providers = []map[string]any{{
		"id":            "my-upstream-provider",
		"name":          "My Upstream Provider",
		"template_name": "my-upstream-provider",
		"enabled":       true,
	}}

	configPath := convertCase(t, "token_vault_credential_manager", "2.3",
		patchIssuer(18090, idp.Port))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, tokenVaultImage),
		Config: configPath,
		Env: []string{
			licenseEnv(license),
			"KONG_IDENTITY_SERVICE=" + identity.URL,
		},
	})
	gateway.waitReady()
	proxy := gateway.ProxyURL()
	idp.setGatewayOrigin(proxy)
	page := proxy + "/vuc/okta-ai-se"
	providers := page + "/providers"

	// The page route runs session auth with the anonymous fallback, so the
	// credential manager answers anonymous callers itself: the login page
	// renders, and the data endpoint rejects the caller.
	anon := &http.Client{Timeout: 30 * time.Second}
	loginPage := httpGetNoRedirect(t, anon, page, nil)
	if loginPage.Status != 200 || !strings.Contains(loginPage.Header.Get("Content-Type"), "text/html") ||
		!strings.Contains(loginPage.Body, "Login with Okta") {
		t.Fatalf("anonymous /vuc: got HTTP %d %q\n%s\n%s",
			loginPage.Status, loginPage.Header.Get("Content-Type"), loginPage.Body, gateway.logs(200))
	}
	t.Log("PASS: the page route served the login page")

	resp := httpGetNoRedirect(t, anon, providers, nil)
	if resp.Status != 401 {
		t.Fatalf("anonymous /providers: got HTTP %d, want the credential manager 401\n%s\n%s",
			resp.Status, resp.Body, gateway.logs(200))
	}
	t.Log("PASS: the credential manager rejects anonymous data requests")

	// The login route starts the authorization code flow: 302 to the IdP,
	// with the authorization cookie scoped to the page route path.
	client, _ := newBrowserClient(t, proxy)
	hop := func(u string) httpResponse {
		t.Helper()
		return httpGetNoRedirect(t, client, u, nil)
	}
	r1 := hop(page + "/login")
	if r1.Status != 302 {
		t.Fatalf("login route: got HTTP %d, want 302\n%s\n%s",
			r1.Status, r1.Body, gateway.logs(200))
	}
	r1URL, err := url.Parse(r1.Location())
	if err != nil || r1URL.Host != idpURLHost(idp) {
		t.Fatalf("login route redirected to %q, want the IdP", r1.Location())
	}
	if auth := setCookie(r1, "authorization"); !strings.Contains(auth, "Path=/vuc/okta-ai-se") {
		t.Fatalf("authorization cookie = %q, want Path=/vuc/okta-ai-se", auth)
	}
	t.Log("PASS: the login route started the browser login")

	// The IdP redirects back. The login route completes the exchange (no
	// redirect_uri is configured), and the credential manager serves the
	// connectors page there.
	r2 := hop(r1.Location())
	if r2.Status != 302 {
		t.Fatalf("IdP /authorize: got HTTP %d, want 302\n%s\n%s", r2.Status, r2.Body, gateway.logs(200))
	}
	r3 := hop(r2.Location())
	if r3.Status != 200 || !strings.Contains(r3.Header.Get("Content-Type"), "text/html") ||
		strings.Contains(r3.Body, "Login with Okta") {
		t.Fatalf("post-login login route: got HTTP %d %q\n%s\n%s",
			r3.Status, r3.Header.Get("Content-Type"), r3.Body, gateway.logs(200))
	}
	if session := setCookie(r3, "session"); session == "" {
		t.Fatalf("the completed login sets no session cookie:\n%q", r3.Header.Values("Set-Cookie"))
	}
	t.Log("PASS: the exchange completed on the login route")

	// With the session, the data endpoint serves the aggregated probe.
	api := hop(providers)
	if api.Status != 200 || !strings.Contains(api.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("providers API: got HTTP %d %q\n%s\n%s",
			api.Status, api.Header.Get("Content-Type"), api.Body, gateway.logs(200))
	}

	// The page route now serves the connectors page, not the login page.
	spa := hop(page)
	if spa.Status != 200 || !strings.Contains(spa.Header.Get("Content-Type"), "text/html") ||
		strings.Contains(spa.Body, "Login with Okta") {
		t.Fatalf("post-login /vuc: got HTTP %d %q\n%s\n%s",
			spa.Status, spa.Header.Get("Content-Type"), spa.Body, gateway.logs(200))
	}
	t.Log("PASS: the page route served the connectors page")

	var entries []struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		InitiateURL string `json:"initiate_url"`
	}
	if err := json.Unmarshal([]byte(api.Body), &entries); err != nil || len(entries) != 1 {
		t.Fatalf("providers API returned %q (%v)", api.Body, err)
	}
	if entries[0].ID != "my-upstream-provider" {
		t.Fatalf("provider id = %q, want my-upstream-provider", entries[0].ID)
	}
	if entries[0].Status != "connect" {
		t.Fatalf("provider status = %q, want connect", entries[0].Status)
	}
	if !strings.Contains(entries[0].InitiateURL, "/_vault/callback/initiate?token=") {
		t.Fatalf("initiate_url = %q, want the gateway-rehosted /_vault URL", entries[0].InitiateURL)
	}
	t.Log("PASS: the connectors API served the enrollment probe")

	// The {directory} path segment proves ApplyTokenVaultDatastore put the
	// directory at the top of the plugin config, not under storage.
	if got := identity.sawRequests(http.MethodGet, "/"+identityDirectory+"/v2/vault/providers"); got != 1 {
		t.Fatalf("identity mock saw %d providers calls through /%s, want 1", got, identityDirectory)
	}
	t.Log("PASS: the credential manager reached Identity through the directory path")

	// A bogus session cookie falls back to the anonymous consumer, so the
	// credential manager rejects the request rather than serving the API.
	bogus := &http.Client{Timeout: 30 * time.Second}
	resp = httpGetNoRedirect(t, bogus, providers, map[string]string{
		"Cookie": "session=forged",
	})
	if resp.Status == 200 {
		t.Fatalf("forged session served the providers API:\n%s", resp.Body)
	}
	t.Log("PASS: a forged session was rejected")
}

// testTokenVaultManagerSessionEnrollment drives the strategy redirect_uri
// topology: the manager login completes on the out-of-band /callback route,
// which mints the browser session. An enrollment link (the URL an MCP tool
// surfaces) must then start the enrollment with that session, not bounce
// back to the credential manager.
func testTokenVaultManagerSessionEnrollment(t *testing.T, license string) {
	idp := startIDPMock(t)
	idp.requireClientAuth.Store(false) // a public client, like the user's Okta app
	identity := startIdentityMock(t)
	identity.AuthURLBase = idp.URL
	identity.TokenEndpoint = idp.URL + "/token"

	configPath := convertCase(t, "token_vault_manager_session_enrollment", "2.3",
		patchIssuer(18090, idp.Port))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, tokenVaultImage),
		Config: configPath,
		Env: []string{
			licenseEnv(license),
			"KONG_IDENTITY_SERVICE=" + identity.URL,
		},
		ProxyPort: 8000,
	})
	gateway.waitReady()
	proxy := gateway.ProxyURL()
	idp.setGatewayOrigin(proxy)

	client, _ := newBrowserClient(t, proxy)
	hop := func(u string) httpResponse {
		t.Helper()
		return httpGetNoRedirect(t, client, u, nil)
	}

	// The manager login starts on the login route, which sends the
	// authorization cookie to the strategy redirect_uri path.
	r1 := hop(proxy + "/vuc/okta-ai-se/login")
	if r1.Status != 302 {
		t.Fatalf("login route: got HTTP %d, want 302\n%s\n%s",
			r1.Status, r1.Body, gateway.logs(200))
	}
	if auth := setCookie(r1, "authorization"); !strings.Contains(auth, "Path=/callback") {
		t.Fatalf("authorization cookie = %q, want Path=/callback", auth)
	}

	// The IdP redirects to the strategy redirect_uri, where the out-of-band
	// route completes the exchange and bounces the browser to the page.
	r2 := hop(r1.Location())
	if r2.Status != 302 {
		t.Fatalf("IdP /authorize: got HTTP %d, want 302\n%s\n%s", r2.Status, r2.Body, gateway.logs(200))
	}
	r2URL, r2Err := url.Parse(r2.Location())
	if r2Err != nil || r2URL.Path != "/callback" {
		t.Fatalf("IdP redirected to %q, want the /callback redirect_uri", r2.Location())
	}
	r3 := hop(r2.Location())
	r3URL, r3Err := url.Parse(r3.Location())
	if r3.Status != 302 || r3Err != nil || r3URL.Path != "/vuc/okta-ai-se" {
		t.Fatalf("callback completion: got HTTP %d -> %q, want 302 to the page route\n%s\n%s",
			r3.Status, r3.Location(), r3.Body, gateway.logs(200))
	}
	if session := setCookie(r3, "session"); session == "" {
		t.Fatalf("the completion sets no session cookie:\n%q", r3.Header.Values("Set-Cookie"))
	}
	r4 := hop(r3.Location())
	if r4.Status != 200 || !strings.Contains(r4.Header.Get("Content-Type"), "text/html") ||
		strings.Contains(r4.Body, "Login with Okta") {
		t.Fatalf("post-login page: got HTTP %d %q\n%s\n%s",
			r4.Status, r4.Header.Get("Content-Type"), r4.Body, gateway.logs(200))
	}
	t.Log("PASS: the manager login completed on the out-of-band /callback route")

	// The enrollment link must start the enrollment with the live session,
	// not serve or bounce to the credential manager.
	r5 := hop(proxy + "/_vault/callback/initiate?token=" + mockEnrollmentBlob)
	wantAuthURL := idp.URL + "/vault-authorize?state=" + url.QueryEscape(mockStateToken())
	if r5.Status != 302 || r5.Location() != wantAuthURL {
		t.Fatalf("enrollment link with a live session: got HTTP %d -> %q, want 302 to %q\n%s\n%s",
			r5.Status, r5.Location(), wantAuthURL, r5.Body, gateway.logs(200))
	}
	resolve := identity.calls("/v2/vault/enrollment/resolve")
	if len(resolve) != 1 {
		t.Fatalf("identity mock saw %d enrollment/resolve calls, want 1", len(resolve))
	}
	if got := resolve[0].Get("token"); got != mockEnrollmentBlob {
		t.Fatalf("resolve token = %q, want the enrollment blob", got)
	}
	tokenCalls := idp.tokenCalls()
	if len(tokenCalls) != 1 {
		t.Fatalf("idp mock saw %d /token calls, want 1 (the manager login)", len(tokenCalls))
	}
	if got := resolve[0].Get("subject_token"); got != tokenCalls[0].AccessToken {
		t.Fatalf("resolve subject_token = %q, want the session access token %q",
			got, tokenCalls[0].AccessToken)
	}
	t.Log("PASS: the enrollment link started the enrollment with the manager session")

	// The enrollment completes under the session: the provider authorization
	// redirects back, and the callback handler seals the credential.
	r6 := hop(r5.Location())
	if r6.Status != 302 {
		t.Fatalf("IdP /vault-authorize: got HTTP %d, want 302\n%s\n%s", r6.Status, r6.Body, gateway.logs(200))
	}
	r7 := hop(r6.Location())
	if r7.Status != 200 || !strings.Contains(r7.Body, "Authorization complete") {
		t.Fatalf("vault callback: got HTTP %d, want the completion page\n%s\n%s",
			r7.Status, r7.Body, gateway.logs(200))
	}
	t.Log("PASS: the enrollment completed under the manager session")
}

func idpURLHost(idp *idpMock) string {
	u, err := url.Parse(idp.URL)
	if err != nil {
		return ""
	}
	return u.Host
}
