package revert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gperanich/ai-deck-converter/convert"
	"gopkg.in/yaml.v3"
)

// TestRoundTrip verifies that reverting the forward converter's output and
// converting it again reproduces the original decK config byte-for-byte, for
// every forward golden case. Source-only metadata (display_name, enabled, the
// original capability spellings) is lossy by design, but everything that
// reaches Kong must survive the round trip — and a clean trip must produce no
// warnings.
func TestRoundTrip(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "convert", "testdata", "*"))
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
			opts := loadForwardOptions(t, dir)
			if opts.OutputMode == "db-less" {
				t.Skip("db-less forward cases are not revertible decK fixtures")
			}

			deck1, err := os.ReadFile(filepath.Join(dir, "expected.yaml"))
			if err != nil {
				t.Fatalf("read forward golden: %v", err)
			}

			aigwYAML, warnings, err := Revert(deck1, Options{})
			if err != nil {
				t.Fatalf("revert: %v", err)
			}
			for _, w := range warnings {
				t.Errorf("unexpected revert warning: %s", w)
			}

			deck2, warnings, err := convert.Convert(aigwYAML, convert.Options{})
			if err != nil {
				t.Fatalf("re-convert: %v", err)
			}
			for _, w := range warnings {
				t.Errorf("unexpected convert warning: %s", w)
			}

			if string(deck1) != string(deck2) {
				t.Errorf("round trip mismatch for %s\n--- original ---\n%s\n--- round-tripped ---\n%s\n--- intermediate aigw ---\n%s", dir, deck1, deck2, aigwYAML)
			}
		})
	}
}

func loadForwardOptions(t *testing.T, dir string) convert.Options {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return convert.Options{}
	}
	if err != nil {
		t.Fatalf("read options: %v", err)
	}
	var opts convert.Options
	if err := yaml.Unmarshal(data, &opts); err != nil {
		t.Fatalf("parse options: %v", err)
	}
	return opts
}
