package convert

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "regenerate golden expected.yaml files")

// TestGolden runs every convert/testdata/<case>/ directory: it converts
// input.yaml and compares the result to expected.yaml. An optional options.yaml
// in the case directory overrides the default conversion Options. Run with
// -update to regenerate the expected files after reviewing changes.
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
			encode := convertYAML
			if opts.OutputMode == "db-less" {
				encode = convertDBLessYAML
			}
			got, _, err := encode(in, opts.Options)
			require.NoError(t, err, "convert")

			expectedPath := filepath.Join(dir, "expected.yaml")
			if *update {
				require.NoError(t, os.WriteFile(expectedPath, got, 0o644), "write golden") //nolint:gosec
				return
			}
			want, err := os.ReadFile(expectedPath)
			require.NoError(t, err, "read golden (run -update to create)")
			require.Equal(t, string(want), string(got), "output mismatch for %s", dir)
		})
	}
}

// goldenOptions adds output_mode to Options. It selects the expected.yaml
// layout: "db-less", or decK when empty.
type goldenOptions struct {
	Options    `yaml:",inline"`
	OutputMode string `yaml:"output_mode"`
}

func loadOptions(t *testing.T, dir string) goldenOptions {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return goldenOptions{}
	}
	require.NoError(t, err, "read options")
	var opts goldenOptions
	require.NoError(t, yaml.Unmarshal(data, &opts), "parse options")
	return opts
}
