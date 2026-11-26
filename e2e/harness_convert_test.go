//go:build e2e

package e2e

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
)

func TestImageReleaseVersion(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"kong/kong-ai-gateway:2.3.0", "2.3"},
		{"kong/kong-ai-gateway:2.3", "2.3"},
		{"kong/kong-ai-gateway-dev:2.1.0-rc.4", "2.1"},
		{"kong/kong-ai-gateway:2.3-dbf5bbb", "2.3"},
		{"registry.local:5000/kong/kong-ai-gateway:2.4.0", "2.4"},
		{"kong/kong-ai-gateway-dev:aigw-master", ""},
		{"kong/kong-ai-gateway-dev:latest", ""},
		{"kong/kong-ai-gateway-dev:45610199308-40a8a7d", ""},
		{"registry.local:5000/kong/kong-ai-gateway", ""},
		{"", ""},
	}
	for _, c := range cases {
		got, ok := imageReleaseVersion(c.image)
		if c.want == "" {
			if ok {
				t.Errorf("imageReleaseVersion(%q) = %s, want no version", c.image, got)
			}
			continue
		}
		want, err := aimap.ParseVersion(c.want)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || got != want {
			t.Errorf("imageReleaseVersion(%q) = %s, %v, want %s", c.image, got, ok, want)
		}
	}
}
