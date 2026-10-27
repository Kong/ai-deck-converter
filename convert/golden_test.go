package convert

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// testOptions targets the newest known release, so no feature gate applies.
var testOptions = Options{MinRuntimeVersion: aimap.LatestVersion.String()}

var update = flag.Bool("update", false, "regenerate golden expected.yaml and issues.yaml files")

// TestGolden runs every convert/testdata/<case>/ directory: it converts
// input.yaml and compares the result to expected.yaml. It compares the issues
// to issues.yaml. A case without issues has no issues.yaml. An optional
// options.yaml in the case directory overrides the default conversion Options.
// The default MinRuntimeVersion is the newest known release.
// Run with -update to regenerate the expected files after reviewing changes.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob("testdata/*")
	require.NoError(t, err)
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			in, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
			require.NoError(t, err, "read input")
			opts := loadOptions(t, dir)
			encode := encodeYAML
			if opts.OutputMode == "db-less" {
				encode = encodeDBLessYAML
			}
			got, issues, err := encode(in, opts.Options)
			require.NoError(t, err, "convert")
			gotIssues := issuesYAML(t, issues)

			expectedPath := filepath.Join(dir, "expected.yaml")
			issuesPath := filepath.Join(dir, "issues.yaml")
			if *update {
				require.NoError(t, os.WriteFile(expectedPath, got, 0o644), "write golden") //nolint:gosec
				if gotIssues == "" {
					require.NoError(t, removeIfExists(issuesPath), "remove issues golden")
				} else {
					require.NoError(t, os.WriteFile(issuesPath, []byte(gotIssues), 0o644), "write issues golden") //nolint:gosec
				}
				return
			}
			want, err := os.ReadFile(expectedPath)
			require.NoError(t, err, "read golden (run -update to create)")
			require.Equal(t, string(want), string(got), "output mismatch for %s", dir)

			wantIssues, err := os.ReadFile(issuesPath)
			if !os.IsNotExist(err) {
				require.NoError(t, err, "read issues golden")
			}
			require.Equal(t, string(wantIssues), gotIssues, "issues mismatch for %s", dir)
		})
	}
}

func issuesYAML(t *testing.T, issues []ConversionIssue) string {
	t.Helper()
	if len(issues) == 0 {
		return ""
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	require.NoError(t, enc.Encode(issues), "marshal issues")
	require.NoError(t, enc.Close(), "marshal issues")
	return buf.String()
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// goldenOptions adds output_mode to Options. It selects the expected.yaml
// layout: "db-less", or decK when empty.
type goldenOptions struct {
	Options    `yaml:",inline"`
	OutputMode string `yaml:"output_mode"`
}

func loadOptions(t *testing.T, dir string) goldenOptions {
	t.Helper()
	opts := goldenOptions{Options: testOptions}
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return opts
	}
	require.NoError(t, err, "read options")
	require.NoError(t, yaml.Unmarshal(data, &opts), "parse options")
	return opts
}
