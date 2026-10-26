//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func httpGet(url string) (int, http.Header, string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, nil, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

type httpResponse struct {
	Status int
	Header http.Header
	Body   string
}

// Location returns the response's redirect target, or "" when absent.
func (r httpResponse) Location() string {
	return r.Header.Get("Location")
}

// setCookie returns the first Set-Cookie attribute string for a name.
func setCookie(resp httpResponse, name string) string {
	for _, c := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, name+"=") {
			return c
		}
	}
	return ""
}

// httpGetNoRedirect performs a GET that never follows redirects, so a case
// can assert on Location and Set-Cookie.
func httpGetNoRedirect(t *testing.T, client *http.Client, url string, headers map[string]string) httpResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building request for %s: %v", url, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client = clientWithNoRedirect(client)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body from %s: %v", url, err)
	}
	return httpResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(body)}
}

// httpGetFollow performs a GET that follows redirects, returning the final
// response of the chain.
func httpGetFollow(t *testing.T, client *http.Client, url string) httpResponse {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body from %s: %v", url, err)
	}
	return httpResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(body)}
}

func clientWithNoRedirect(client *http.Client) *http.Client {
	clone := *client
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

// newBrowserClient returns a cookie-jar client that follows the same
// redirects a browser would. It dials host.docker.internal as 127.0.0.1, so
// one issuer string stays valid inside and outside the container, and it
// dials the gateway's internal port 8000 as the published proxy port, since
// openid-connect builds redirect URIs from the listener port. The jar is
// returned so a case can assert cookie paths and values.
func newBrowserClient(t *testing.T, proxyURL string) (*http.Client, *cookiejar.Jar) {
	t.Helper()
	proxyPort := ""
	if u, err := url.Parse(proxyURL); err == nil {
		proxyPort = u.Port()
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("creating cookie jar: %v", err)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr == nil {
				if host == "host.docker.internal" {
					host = "127.0.0.1"
				}
				if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() && port == "8000" && proxyPort != "" {
					port = proxyPort
				}
				addr = net.JoinHostPort(host, port)
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, addr)
		},
	}
	client := &http.Client{Jar: jar, Transport: transport, Timeout: 30 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after %d redirects", len(via))
		}
		return nil
	}
	return client, jar
}

func httpPost(t *testing.T, url string, headers map[string]string, body string) httpResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request for %s: %v", url, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body from %s: %v", url, err)
	}
	return httpResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(respBody)}
}

func headerValue(h http.Header, name string) string {
	if v := h.Get(name); v != "" {
		return v
	}
	for k, vs := range h {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func requireStatus(t *testing.T, resp httpResponse, want int, context string) {
	t.Helper()
	if resp.Status != want {
		t.Fatalf("%s: got HTTP %d, want %d\nresponse body:\n%s", context, resp.Status, want, resp.Body)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root (no go.mod)")
		}
		dir = parent
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
