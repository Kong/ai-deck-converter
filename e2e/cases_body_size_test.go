//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// bodySizeModel is one model of the model_body_size cases. limit is the
// max_request_body_size that the loaded ai-proxy-advanced must have.
type bodySizeModel struct {
	name  string
	limit int
	// selectorLimit is the ai-model-selector limit. It is never below 8 MB.
	selectorLimit int
}

// testModelBodySize proves the max_request_body_size that each model gets on
// the data plane of one release. The case input has no limit on most models.
// This is the input after the AI Gateway API stops sending its 8 MB default.
func testModelBodySize(t *testing.T, license, caseName, minRuntimeVersion, image string, models []bodySizeModel) {
	mock := startMockUpstream(t)
	configPath := convertCase(t, caseName, minRuntimeVersion, patchMockUpstream(mock.Port, mockAuthHeaderValue))

	gateway := startGateway(t, GatewayOptions{
		Image:  gatewayImage(t, image),
		Config: configPath,
		Env:    []string{licenseEnv(license)},
	})
	gateway.waitReady()

	loaded := loadedProxyBodySizes(t, gateway)
	for _, m := range models {
		got, ok := loaded[m.name]
		if !ok {
			t.Fatalf("the gateway loaded no ai-proxy-advanced for model %q; loaded: %v", m.name, loaded)
		}
		if got != m.limit {
			t.Fatalf("model %q: loaded ai-proxy-advanced has max_request_body_size %d, want %d", m.name, got, m.limit)
		}
	}
	t.Logf("PASS: the %s gateway loaded the expected ai-proxy-advanced limits %v", minRuntimeVersion, loaded)

	for _, m := range models {
		t.Run("model="+m.name, func(t *testing.T) {
			requireBodyReachesUpstream(t, gateway, mock, m.name, 1024)

			// A body above 8 MB is the request that the provider limit lets in.
			if m.limit > aimap.DefaultMaxBodySize {
				requireBodyReachesUpstream(t, gateway, mock, m.name, aimap.DefaultMaxBodySize+1<<20)
			}

			resp, reached := postBodyOfSize(t, gateway, mock, m.name, m.limit+64<<10)
			if reached {
				t.Fatalf("model %q: a body above the %d byte limit reached the upstream", m.name, m.limit)
			}
			// ai-model-selector reads the body first. When the body is above its
			// limit, it selects no model and ai-proxy-advanced does not run. The
			// request then goes to the placeholder Service and gets 503.
			if m.limit == m.selectorLimit {
				requireStatus(t, resp, 503, fmt.Sprintf("model %q: a body above the selector limit", m.name))
			} else {
				requireStatus(t, resp, 400, fmt.Sprintf("model %q: a body above the proxy limit", m.name))
				want := fmt.Sprintf("current limit: %d bytes", m.limit)
				if !strings.Contains(resp.Body, want) {
					t.Fatalf("model %q: the rejection does not contain %q:\n%.500s", m.name, want, resp.Body)
				}
			}
			t.Logf("PASS: model %q accepts bodies up to its limit and rejects a body above %d bytes with %d",
				m.name, m.limit, resp.Status)
		})
	}
}

// loadedProxyBodySizes returns the max_request_body_size of each loaded
// ai-proxy-advanced plugin, by the model alias of its first target. The data
// plane fills in its schema default when the config has no value.
func loadedProxyBodySizes(t *testing.T, gateway *Gateway) map[string]int {
	t.Helper()
	status, _, body := httpGet(gateway.AdminURL() + "/plugins?size=1000")
	if status != 200 {
		t.Fatalf("GET /plugins returned %d:\n%s", status, body)
	}
	var plugins struct {
		Data []struct {
			Name   string `json:"name"`
			Config struct {
				MaxRequestBodySize int `json:"max_request_body_size"`
				Targets            []struct {
					Model struct {
						ModelAlias string `json:"model_alias"`
					} `json:"model"`
				} `json:"targets"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &plugins); err != nil {
		t.Fatalf("decoding GET /plugins: %v\n%s", err, body)
	}
	sizes := map[string]int{}
	for _, p := range plugins.Data {
		if p.Name != "ai-proxy-advanced" || len(p.Config.Targets) == 0 {
			continue
		}
		sizes[p.Config.Targets[0].Model.ModelAlias] = p.Config.MaxRequestBodySize
	}
	return sizes
}

func requireBodyReachesUpstream(t *testing.T, gateway *Gateway, mock *mockUpstream, model string, size int) {
	t.Helper()
	resp, reached := postBodyOfSize(t, gateway, mock, model, size)
	if !reached {
		t.Fatalf("model %q: a %d byte body did not reach the upstream; status %d:\n%.500s",
			model, size, resp.Status, resp.Body)
	}
}

// postBodyOfSize sends a chat request of exactly size bytes to the route of
// model. It tells if the request reached the mock upstream.
func postBodyOfSize(t *testing.T, gateway *Gateway, mock *mockUpstream, model string, size int) (httpResponse, bool) {
	t.Helper()
	prefix := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"`, model)
	suffix := `"}]}`
	pad := size - len(prefix) - len(suffix)
	if pad < 0 {
		t.Fatalf("a %d byte body is too small for model %q", size, model)
	}
	before := len(mock.hits())
	resp := httpPost(t, gateway.ProxyURL()+"/"+model+"/chat/completions",
		map[string]string{"Content-Type": "application/json"},
		prefix+strings.Repeat("a", pad)+suffix)
	return resp, len(mock.hits()) > before
}
