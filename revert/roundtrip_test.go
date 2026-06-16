package revert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/Kong/ai-deck-converter/convert"
)

// TestRoundTrip verifies that reverting the forward converter's output and
// converting it again reproduces the original decK config byte-for-byte, for
// every forward golden case. Source-only metadata (display_name, enabled, the
// original capability spellings) is lossy by design, but everything that
// reaches Kong must survive the round trip — and a clean trip must produce no
// warnings.
func TestRoundTrip(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "convert", "testdata", "*"))
	require.NoError(t, err)
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
			require.NoError(t, err, "read forward golden")

			aigwYAML, warnings, err := Revert(deck1, Options{})
			require.NoError(t, err, "revert")
			require.Empty(t, warnings, "unexpected revert warnings")

			deck2, warnings, err := convert.Convert(aigwYAML, convert.Options{})
			require.NoError(t, err, "re-convert")
			require.Empty(t, warnings, "unexpected convert warnings")

			require.Equalf(t, string(deck1), string(deck2),
				"round trip mismatch for %s\n--- intermediate aigw ---\n%s", dir, aigwYAML)
		})
	}
}

func loadForwardOptions(t *testing.T, dir string) convert.Options {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return convert.Options{}
	}
	require.NoError(t, err, "read options")
	var opts convert.Options
	require.NoError(t, yaml.Unmarshal(data, &opts), "parse options")
	return opts
}
