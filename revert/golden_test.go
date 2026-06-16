package revert

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "regenerate golden expected.yaml files")

// TestGolden runs every revert/testdata/<case>/ directory: it reverts
// input.yaml (a Kong decK config) and compares the result to expected.yaml (an
// AI Gateway entity-model config). An optional options.yaml in the case
// directory overrides the default reversal Options. Run with -update to
// regenerate the expected files after reviewing changes.
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
			got, _, err := Revert(in, opts)
			require.NoError(t, err, "revert")

			expectedPath := filepath.Join(dir, "expected.yaml")
			if *update {
				require.NoError(t, os.WriteFile(expectedPath, got, 0o644), "write golden")
				return
			}
			want, err := os.ReadFile(expectedPath)
			require.NoError(t, err, "read golden (run -update to create)")
			require.Equal(t, string(want), string(got), "output mismatch for %s", dir)
		})
	}
}

func loadOptions(t *testing.T, dir string) Options {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return Options{}
	}
	require.NoError(t, err, "read options")
	var opts Options
	require.NoError(t, yaml.Unmarshal(data, &opts), "parse options")
	return opts
}
