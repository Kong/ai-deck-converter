package revert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/Kong/ai-deck-converter/convert"
)

// unexpectedWarnings returns the warnings in got that don't match (by
// substring) any entry in allowed.
func unexpectedWarnings(got, allowed []string) []string {
	var out []string
	for _, w := range got {
		matched := false
		for _, a := range allowed {
			if strings.Contains(w, a) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, w)
		}
	}
	return out
}

// expectedRoundTripWarnings lists, per testdata case, substrings of warnings
// that are expected to survive a round trip because they flag a real,
// currently-unaddressed source config issue (not something the round trip
// itself introduces) — e.g. a model with acls but no authentication plugin.
// Any warning not matched here still fails the test.
var expectedRoundTripWarnings = map[string][]string{
	"05_identity_and_policies": {"has acls configured but no authentication plugin"},
	"08_kitchen_sink":          {"has acls configured but no authentication plugin"},
}

// TestRoundTrip verifies that reverting the forward converter's output and
// converting it again reproduces the original decK config byte-for-byte, for
// every forward golden case. Source-only metadata (display_name, enabled, the
// original capability spellings) is lossy by design, but everything that
// reaches Kong must survive the round trip — and a clean trip must produce no
// warnings beyond the case's known, pre-existing ones (see
// expectedRoundTripWarnings).
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
			require.Empty(t, unexpectedWarnings(warnings, expectedRoundTripWarnings[filepath.Base(dir)]),
				"unexpected convert warnings")

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
