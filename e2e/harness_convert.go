//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/convert"
	"gopkg.in/yaml.v3"
)

var flagUpdate bool

// convertCase converts e2e/testdata/<caseName>/input.yaml, snapshot-checks the
// result against the checked-in expected.yaml, and mounts it into a gateway
// container. When patch is set the fixture is converted twice: unpatched for
// the snapshot (which stays port- and run-stable) and patched for the running
// gateway. On -update the snapshot is (re)written instead of compared.
func convertCase(t *testing.T, caseName string, patch func(src []byte) ([]byte, error)) string {
	t.Helper()

	caseDir := filepath.Join(repoRoot(t), "e2e", "testdata", caseName)
	src, err := os.ReadFile(filepath.Join(caseDir, "input.yaml"))
	if err != nil {
		t.Fatalf("reading input.yaml: %v", err)
	}

	out, warnings, err := convert.Convert(src, convert.Options{OutputMode: "db-less"})
	if err != nil {
		t.Fatalf("converting %s: %v", caseDir, err)
	}
	for _, w := range warnings {
		t.Logf("conversion warning: %s", w)
	}

	expectedPath := filepath.Join(caseDir, "expected.yaml")
	if flagUpdate {
		writeFile(t, expectedPath, string(out))
		t.Logf("updated snapshot %s", expectedPath)
	} else {
		expected, err := os.ReadFile(expectedPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reading snapshot: %v", err)
			}
			t.Fatalf("no snapshot at %s; run: go test -tags=e2e ./e2e -update", expectedPath)
		}
		if !bytes.Equal(bytes.TrimSpace(expected), bytes.TrimSpace(out)) {
			writeArtifact(t, "converted.yaml", string(out))
			t.Fatalf("converter output does not match snapshot %s\nrun: go test -tags=e2e ./e2e -update\nthe actual output was written to e2e/artifacts/%s/converted.yaml",
				expectedPath, sanitize(t.Name()))
		}
	}

	converted := out
	if patch != nil {
		patchedSrc, err := patch(src)
		if err != nil {
			t.Fatalf("patching input.yaml: %v", err)
		}
		converted, _, err = convert.Convert(patchedSrc, convert.Options{OutputMode: "db-less"})
		if err != nil {
			t.Fatalf("converting patched %s: %v", caseDir, err)
		}
	}

	configPath := filepath.Join(repoRoot(t), "e2e", "artifacts", sanitize(t.Name()), "kong.yaml")
	writeFile(t, configPath, string(converted))
	writeArtifact(t, "converted.yaml", string(converted))
	return configPath
}

func writeArtifact(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "e2e", "artifacts", sanitize(t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("creating artifact dir %s: %v", dir, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Logf("writing artifact %s: %v", name, err)
	}
}

func mappingChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return mappingChild(n.Content[0], key)
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func asMapping(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return n.Content[0]
	}
	return n
}

func setScalar(n *yaml.Node, key, value string) {
	m := asMapping(n)
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Value = value
			m.Content[i+1].Tag = "!!str"
			m.Content[i+1].Style = 0
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	m.Content = append(m.Content, keyNode, valNode)
}

func remarshal(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// patchMockUpstream points every model target at the local mock upstream and
// swaps the unresolved vault reference in the provider auth header for a fixed
// secret the mock can assert on.
func patchMockUpstream(port int, headerValue string) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return nil, fmt.Errorf("parsing input.yaml: %w", err)
		}
		upstream := fmt.Sprintf("http://host.docker.internal:%d", port)

		models := mappingChild(&doc, "models")
		if models == nil || models.Kind != yaml.SequenceNode {
			return nil, errors.New("input.yaml has no models list")
		}
		for _, model := range models.Content {
			targets := mappingChild(model, "targets")
			if targets == nil || targets.Kind != yaml.SequenceNode {
				continue
			}
			for _, target := range targets.Content {
				setScalar(mappingChild(target, "config"), "upstream_url", upstream)
			}
		}

		providers := mappingChild(&doc, "model_providers")
		if providers == nil || providers.Kind != yaml.SequenceNode {
			return nil, errors.New("input.yaml has no model_providers list")
		}
		for _, provider := range providers.Content {
			headers := mappingChild(mappingChild(mappingChild(provider, "config"), "auth"), "headers")
			if headers == nil || headers.Kind != yaml.SequenceNode || len(headers.Content) == 0 {
				continue
			}
			setScalar(headers.Content[0], "value", headerValue)
		}
		return remarshal(&doc)
	}
}

// patchMCPPort rewrites the mock-upstream port in mcp_servers upstream_url
// values (the fixtures pin a fixed port; the harness allocates a free one).
func patchMCPPort(oldPort, newPort int) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return nil, fmt.Errorf("parsing input.yaml: %w", err)
		}
		servers := mappingChild(&doc, "mcp_servers")
		if servers == nil || servers.Kind != yaml.SequenceNode {
			return nil, errors.New("input.yaml has no mcp_servers list")
		}
		for _, server := range servers.Content {
			if u := mappingChild(server, "upstream_url"); u != nil && u.Kind == yaml.ScalarNode {
				u.Value = strings.Replace(u.Value, fmt.Sprintf(":%d", oldPort), fmt.Sprintf(":%d", newPort), 1)
			}
		}
		return remarshal(&doc)
	}
}
