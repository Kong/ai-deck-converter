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
// minRuntimeVersion is the minor release of the case's default image.
func convertCase(t *testing.T, caseName, minRuntimeVersion string, patch func(src []byte) ([]byte, error)) string {
	t.Helper()

	caseDir := filepath.Join(repoRoot(t), "e2e", "testdata", caseName)
	src, err := os.ReadFile(filepath.Join(caseDir, "input.yaml"))
	if err != nil {
		t.Fatalf("reading input.yaml: %v", err)
	}

	out, err := convertDBLess(t, src, minRuntimeVersion)
	if err != nil {
		t.Fatalf("converting %s: %v", caseDir, err)
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
		converted, err = convertDBLess(t, patchedSrc, minRuntimeVersion)
		if err != nil {
			t.Fatalf("converting patched %s: %v", caseDir, err)
		}
	}

	configPath := filepath.Join(repoRoot(t), "e2e", "artifacts", sanitize(t.Name()), "kong.yaml")
	writeFile(t, configPath, string(converted))
	writeArtifact(t, "converted.yaml", string(converted))
	return configPath
}

// convertDBLess logs every conversion issue. An Error issue fails the test.
func convertDBLess(t *testing.T, src []byte, minRuntimeVersion string) ([]byte, error) {
	t.Helper()
	result, err := convert.Convert(src, convert.Options{
		MinRuntimeVersion: minRuntimeVersion,
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range result.Issues {
		t.Logf("conversion issue: %s", issue)
	}
	if result.HasErrors() {
		t.Fatalf("conversion reported Error issues")
	}
	return result.Output.ToDBLess().ToYAML()
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

// patchAll applies each patch in order.
func patchAll(patches ...func([]byte) ([]byte, error)) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var err error
		for _, patch := range patches {
			src, err = patch(src)
			if err != nil {
				return nil, err
			}
		}
		return src, nil
	}
}

// patchVaultedTargets points every model target at the mock upstream. Unlike
// patchMockUpstream it does not touch provider auth headers, which a vaulted
// provider does not have.
func patchVaultedTargets(port int) func([]byte) ([]byte, error) {
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
		return remarshal(&doc)
	}
}

// patchIssuer rewrites the port of every auth_strategies[].config.issuer
// and every policies[].config.issuer (the fixtures pin a fixed port; the
// harness allocates a free one).
func patchIssuer(oldPort, newPort int) func([]byte) ([]byte, error) {
	return func(src []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(src, &doc); err != nil {
			return nil, fmt.Errorf("parsing input.yaml: %w", err)
		}
		for _, section := range []string{"auth_strategies", "policies"} {
			entries := mappingChild(&doc, section)
			if entries == nil || entries.Kind != yaml.SequenceNode {
				continue
			}
			for _, entry := range entries.Content {
				issuer := mappingChild(mappingChild(entry, "config"), "issuer")
				if issuer != nil && issuer.Kind == yaml.ScalarNode {
					issuer.Value = strings.Replace(issuer.Value, fmt.Sprintf(":%d", oldPort), fmt.Sprintf(":%d", newPort), 1)
				}
			}
		}
		return remarshal(&doc)
	}
}
