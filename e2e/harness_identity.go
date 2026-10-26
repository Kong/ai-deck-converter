//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// identityDirectory names the Token Vault directory in every vault fixture.
// The enrollment blobs and state tokens below carry it as a claim.
const identityDirectory = "my-directory"

// vaultDirectoryToken builds the JWT-shaped token Kong Identity mints for an
// enrollment blob or a state token. Kong reads the claims without verifying
// the signature (kong/llm/token_vault/enrollment.lua claims_from_state_token).
func vaultDirectoryToken(credentialID string) string {
	claims, _ := json.Marshal(map[string]string{
		"org_id":         "e2e-org",
		"directory_name": identityDirectory,
		"credential_id":  credentialID,
	})
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(claims) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("unverified"))
}

// mockEnrollmentBlob is the signed enrollment blob Identity hands out in
// x_kong_enrollment_url. The gateway rehosts it onto /_vault/callback/initiate.
var mockEnrollmentBlob = vaultDirectoryToken("e2e-cred")

// mockStateToken is the state token Identity returns from enrollment/resolve.
// It doubles as the OAuth state the IdP echoes back on the callback.
func mockStateToken() string {
	return vaultDirectoryToken("e2e-cred-state")
}

type identityCall struct {
	Method string
	Path   string
	Form   url.Values
}

// identityMock stands in for the Kong Identity vault endpoints the Token
// Vault plugins call (kong/tools/konnect/principal/client.lua). Enrolled
// false makes POST /token answer x_kong_enrollment_required.
type identityMock struct {
	URL  string // http://host.docker.internal:<port>, for KONG_IDENTITY_SERVICE
	Port int

	Enrolled      atomic.Bool
	Secret        string           // plaintext sealed into the exchange response
	BearerMethods []string         // x-kong-bearer-methods-supported; nil omits the field
	Providers     []map[string]any // rows served by GET /{directory}/v2/vault/providers

	// AuthURLBase is the IdP URL enrollment/resolve bakes into auth_url
	// (the case points it at the OIDC mock). TokenEndpoint is what
	// enrollment/resolve-state returns so the gateway exchanges the code
	// against the IdP mock.
	AuthURLBase   string
	TokenEndpoint string

	t *testing.T

	mu      sync.Mutex
	request []identityCall
}

func startIdentityMock(t *testing.T) *identityMock {
	t.Helper()
	m := &identityMock{t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v2/vault/providers") {
			m.record(r, nil)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": m.Providers,
				"meta": map[string]any{"page": map[string]any{"next": nil}},
			})
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		m.record(r, r.PostForm)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/v2/vault/token"):
			if !m.Enrolled.Load() {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":                 "x_kong_enrollment_required",
					"x_kong_enrollment_url": "http://identity.invalid/enroll?token=" + mockEnrollmentBlob,
				})
				return
			}
			resp := map[string]any{
				"access_token":      sealTokenVaultCredential(m.t, m.Secret),
				"issued_token_type": "urn:konghq.com:params:oauth:token-type:static_secret",
				"expires_in":        300,
			}
			if m.BearerMethods != nil {
				resp["x-kong-bearer-methods-supported"] = m.BearerMethods
			}
			_ = json.NewEncoder(w).Encode(resp)

		case strings.HasSuffix(r.URL.Path, "/v2/vault/enrollment/resolve"):
			state := mockStateToken()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"auth_url":    m.AuthURLBase + "/vault-authorize?state=" + url.QueryEscape(state),
				"state_token": state,
			})

		case strings.HasSuffix(r.URL.Path, "/v2/vault/enrollment/resolve-state"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":      "vault-client",
				"client_secret":  "vault-secret",
				"token_endpoint": m.TokenEndpoint,
			})

		case strings.HasSuffix(r.URL.Path, "/v2/vault/enrollment/complete"):
			_ = json.NewEncoder(w).Encode(map[string]any{})

		default:
			http.Error(w, "no identity mock route for "+r.URL.Path, http.StatusNotFound)
		}
	})

	m.Port = serveOnFreePort(t, mux)
	m.URL = fmt.Sprintf("http://host.docker.internal:%d", m.Port)
	return m
}

func (m *identityMock) record(r *http.Request, form url.Values) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.request = append(m.request, identityCall{
		Method: r.Method,
		Path:   r.URL.Path,
		Form:   form,
	})
}

func (m *identityMock) enroll() {
	m.Enrolled.Store(true)
}

// calls returns the recorded form values of every POST whose path ends in
// suffix, in arrival order.
func (m *identityMock) calls(suffix string) []url.Values {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []url.Values
	for _, c := range m.request {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, suffix) {
			out = append(out, c.Form)
		}
	}
	return out
}

// sawRequests counts recorded requests with the method whose path ends in
// suffix.
func (m *identityMock) sawRequests(method, suffix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.request {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			n++
		}
	}
	return n
}
