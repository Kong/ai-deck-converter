//go:build e2e

package e2e

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// idpMock is an OpenID Connect provider with no login page: /authorize
// immediately redirects back with a fresh code. Both /authorize and
// /vault-authorize echo the state they receive. /vault-authorize redirects
// to the gateway's /_vault/callback, so the Token Vault enrollment flow can
// ride through it (the case sets the gateway origin after startGateway).
type idpMock struct {
	URL  string // also the issuer; must match the converter output byte-for-byte
	Port int

	gatewayOrigin atomic.Value // string, set via setGatewayOrigin
	// requireClientAuth makes /token reject a request with no client
	// authentication, the way a confidential client's provider does.
	requireClientAuth atomic.Bool

	mu        sync.Mutex
	token     []idpTokenCall
	lastNonce atomic.Value

	keyOnce sync.Once
	key     *rsa.PrivateKey
}

type idpTokenCall struct {
	Form        url.Values
	AccessToken string
}

func startIDPMock(t *testing.T) *idpMock {
	t.Helper()
	m := &idpMock{}
	// Every case registers a client secret, so every token request must
	// authenticate. A public-client case clears this.
	m.requireClientAuth.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, map[string]any{
				"issuer":                                m.URL,
				"authorization_endpoint":                m.URL + "/authorize",
				"token_endpoint":                        m.URL + "/token",
				"jwks_uri":                              m.URL + "/jwks",
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
				"subject_types_supported":               []string{"public"},
			})

		case "/jwks":
			key := m.signingKey(t)
			writeJSON(w, map[string]any{
				"keys": []map[string]string{{
					"kty": "RSA",
					"use": "sig",
					"alg": "RS256",
					"kid": "e2e-idp-key",
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   "AQAB",
				}},
			})

		case "/authorize", "/vault-authorize":
			q := r.URL.Query()
			if n := q.Get("nonce"); n != "" {
				m.lastNonce.Store(n)
			}
			target := q.Get("redirect_uri")
			if r.URL.Path == "/vault-authorize" {
				// The Token Vault flow: Identity's auth_url points here with
				// the state token; the browser lands on the gateway callback.
				target = m.gatewayOrigin.Load().(string) + "/_vault/callback"
			}
			if target == "" {
				http.Error(w, "missing redirect_uri", http.StatusBadRequest)
				return
			}
			sep := "?"
			if strings.Contains(target, "?") {
				sep = "&"
			}
			target += sep + "code=e2e-code-" + fmt.Sprint(time.Now().UnixNano()) + "&state=" + url.QueryEscape(q.Get("state"))
			http.Redirect(w, r, target, http.StatusFound)

		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			// A confidential client must authenticate. The converter output
			// only drops client authentication for a client with no secret,
			// so a token request with neither a Basic credential nor a
			// client_secret form field is a conversion bug.
			clientID, clientSecret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
			if user, pass, ok := r.BasicAuth(); ok {
				clientID, clientSecret = user, pass
			}
			if m.requireClientAuth.Load() && clientSecret == "" {
				w.WriteHeader(http.StatusUnauthorized)
				writeJSON(w, map[string]any{
					"error":             "invalid_client",
					"error_description": "the token request carried no client authentication",
				})
				return
			}
			access := m.jwt(t, map[string]any{
				"iss": m.URL, "aud": clientID, "sub": "e2e-user",
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
				"jti": fmt.Sprintf("idp-access-%d", time.Now().UnixNano()),
			})
			idClaims := map[string]any{
				"iss": m.URL, "aud": clientID, "sub": "e2e-user",
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
				"jti": fmt.Sprintf("idp-idtoken-%d", time.Now().UnixNano()),
			}
			if nonce, ok := m.lastNonce.Load().(string); ok && nonce != "" {
				idClaims["nonce"] = nonce
			}
			idToken := m.jwt(t, idClaims)
			m.mu.Lock()
			m.token = append(m.token, idpTokenCall{Form: r.PostForm, AccessToken: access})
			m.mu.Unlock()

			writeJSON(w, map[string]any{
				"access_token":  access,
				"id_token":      idToken,
				"refresh_token": "idp-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})

		default:
			http.Error(w, "no idp mock route for "+r.URL.Path, http.StatusNotFound)
		}
	})

	m.Port = serveOnFreePort(t, mux)
	m.URL = fmt.Sprintf("http://host.docker.internal:%d", m.Port)
	return m
}

func (m *idpMock) setGatewayOrigin(origin string) {
	m.gatewayOrigin.Store(origin)
}

// signingKey generates the IdP's RS256 key once per run.
func (m *idpMock) signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	m.keyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generating idp signing key: %v", err)
		}
		m.key = key
	})
	return m.key
}

// tokenCalls returns the form values of every POST to /token, and the access
// token minted for each, in arrival order.
func (m *idpMock) tokenCalls() []idpTokenCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]idpTokenCall(nil), m.token...)
}

// jwt signs the claims with the IdP's RS256 key.
func (m *idpMock) jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	key := m.signingKey(t)

	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "e2e-idp-key"})
	if err != nil {
		t.Fatalf("encoding jwt header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encoding jwt claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing jwt: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
