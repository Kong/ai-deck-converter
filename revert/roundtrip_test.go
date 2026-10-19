package revert

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/convert"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// roundTripSkip lists forward golden cases whose reverse direction is not (yet)
// implemented, so the round-trip gate skips them instead of failing. The
// forward golden itself is still asserted by convert's TestGolden.
var roundTripSkip = map[string]string{
	"92_model_provider_token_vault": "revert does not lift token-vault-exchange or the callback route",
}

// TestRoundTrip verifies that reverting the forward converter's output and
// converting it again reproduces the original decK config byte-for-byte, for
// every forward golden case. Source-only metadata (display_name, enabled, the
// original capability spellings) is lossy by design, but everything that
// reaches Kong must survive the round trip — and a clean trip must produce no
// warnings or issues.
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
			if reason, ok := roundTripSkip[filepath.Base(dir)]; ok {
				t.Skip(reason)
			}
			opts := loadForwardOptions(t, dir)
			if opts.OutputMode == "db-less" {
				t.Skip("db-less forward cases are not revertible decK fixtures")
			}

			deck1, err := os.ReadFile(filepath.Join(dir, "expected.yaml"))
			require.NoError(t, err, "read forward golden")

			aigwYAML, warnings, err := Revert(deck1, Options{})
			require.NoError(t, err, "revert")
			require.Empty(t, warnings, "unexpected revert warnings")

			result, err := convert.Convert(aigwYAML, opts.Options)
			require.NoError(t, err, "re-convert")
			require.Empty(t, unexpectedIssues(t, dir, result.Issues), "unexpected convert issues")
			deck2, err := result.Output.ToYAML()
			require.NoError(t, err, "encode re-converted document")

			require.Equalf(t, string(deck1), string(deck2),
				"round trip mismatch for %s\n--- intermediate aigw ---\n%s", dir, aigwYAML)
		})
	}
}

// unexpectedIssues returns the issues of a re-conversion that are not already
// in the forward golden's issues.yaml. A WARNING whose entities all stay in
// the output (nothing is omitted because of it) recurs identically when the
// round-tripped document is reconverted, so it is not a regression. This
// covers both a routing conflict (both entities stay) and a custom policy's
// priority warning (the policy stays model-scoped either way).
func unexpectedIssues(t *testing.T, dir string, issues []convert.ConversionIssue) []convert.ConversionIssue {
	t.Helper()
	var forward []convert.ConversionIssue
	data, err := os.ReadFile(filepath.Join(dir, "issues.yaml"))
	if !os.IsNotExist(err) {
		require.NoError(t, err, "read forward issues")
		require.NoError(t, yaml.Unmarshal(data, &forward), "parse forward issues")
	}
	wildcard := wildcardModels(t, dir)
	var out []convert.ConversionIssue
	for _, issue := range issues {
		known := issue.Severity == convert.Warning &&
			(slices.ContainsFunc(forward, func(f convert.ConversionIssue) bool { return reflect.DeepEqual(f, issue) }) ||
				wildcard[issue.Source.Name] && wildcardWarning(issue))
		if !known {
			out = append(out, issue)
		}
	}
	return out
}

// wildcardFormat is one entry of a forward model's formats list.
type wildcardFormat struct {
	Type string `yaml:"type"`
}

// wildcardModels returns the names of the forward models with "*" in
// capabilities or formats. The reverter writes their explicit lists, so the
// re-conversion can warn where "*" gave no issue.
func wildcardModels(t *testing.T, dir string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
	require.NoError(t, err, "read forward input")
	var doc struct {
		Models []struct {
			Name         string           `yaml:"name"`
			Capabilities []string         `yaml:"capabilities"`
			Formats      []wildcardFormat `yaml:"formats"`
		} `yaml:"models"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc), "parse forward input")
	out := map[string]bool{}
	for _, m := range doc.Models {
		formatIsWildcard := slices.ContainsFunc(m.Formats, func(f wildcardFormat) bool { return f.Type == aimap.Wildcard })
		if slices.Contains(m.Capabilities, aimap.Wildcard) || formatIsWildcard {
			out[m.Name] = true
		}
	}
	return out
}

// wildcardWarning reports whether issue is one of the warnings that an
// explicit list gives where "*" gives none.
func wildcardWarning(issue convert.ConversionIssue) bool {
	fields := issue.Source.Fields
	if issue.Code == convert.RoutingConflict {
		return slices.Equal(fields, []string{"config.route"})
	}
	return issue.Code == convert.BehaviorMayDiffer && (slices.Equal(fields, []string{"targets"}) ||
		len(fields) == 2 && strings.HasPrefix(fields[0], "capabilities[") && strings.HasPrefix(fields[1], "targets["))
}

// latestConvertOptions targets the newest known release, as the convert
// golden harness does by default.
var latestConvertOptions = convert.Options{MinRuntimeVersion: aimap.LatestVersion.String()}

// forwardOptions mirrors the convert golden options.yaml layout.
type forwardOptions struct {
	convert.Options `yaml:",inline"`
	OutputMode      string `yaml:"output_mode"`
}

func loadForwardOptions(t *testing.T, dir string) forwardOptions {
	t.Helper()
	opts := forwardOptions{Options: latestConvertOptions}
	data, err := os.ReadFile(filepath.Join(dir, "options.yaml"))
	if os.IsNotExist(err) {
		return opts
	}
	require.NoError(t, err, "read options")
	require.NoError(t, yaml.Unmarshal(data, &opts), "parse options")
	return opts
}
