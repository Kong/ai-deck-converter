package revert

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

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
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			in, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
			if err != nil {
				t.Fatalf("read input: %v", err)
			}
			opts := loadOptions(t, dir)
			got, _, err := Revert(in, opts)
			if err != nil {
				t.Fatalf("revert: %v", err)
			}

			expectedPath := filepath.Join(dir, "expected.yaml")
			if *update {
				if err := os.WriteFile(expectedPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read golden (run -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", dir, got, want)
			}
		})
	}
}

func loadOptions(t *testing.T, dir string) Options {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return Options{}
	}
	if err != nil {
		t.Fatalf("read options: %v", err)
	}
	var opts Options
	if err := yaml.Unmarshal(data, &opts); err != nil {
		t.Fatalf("parse options: %v", err)
	}
	return opts
}
