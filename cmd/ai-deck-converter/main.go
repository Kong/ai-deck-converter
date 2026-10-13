// Command ai-deck-converter translates between an AI Gateway entity-model YAML
// config and a Kong Gateway decK declarative YAML config, in either direction.
// The direction is auto-detected from the input (a decK document carries
// _format_version) and can be forced with -direction.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Kong/ai-deck-converter/convert"
	"github.com/Kong/ai-deck-converter/revert"
	"gopkg.in/yaml.v3"
)

// errIssues reports that the output was written but has issues that fail the
// run.
var errIssues = errors.New("conversion reported issues that fail the run")

func main() {
	if err := run(); err != nil {
		if !errors.Is(err, errIssues) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}

func run() error {
	var (
		outPath = flag.String("o", "", "output file path (default: stdout)")
		strict  = flag.Bool("strict", false,
			"exit non-zero on warnings too: to-deck/to-dbless fail on any issue, "+
				"from-deck makes unresolved references and unconvertible entities errors")
		tagPrefix = flag.String("label-tag-prefix", "",
			"prefix for label-derived tags (prepended when converting, stripped when reverting), e.g. aigw/")
		direction = flag.String("direction", "auto",
			"conversion direction: auto, to-deck (AI Gateway -> decK), to-dbless (AI Gateway -> db-less), "+
				"or from-deck (decK -> AI Gateway)")
	)
	flag.Parse()

	in, err := readInput(flag.Arg(0))
	if err != nil {
		return err
	}

	dir := *direction
	if dir == "auto" {
		dir, err = detectDirection(in)
		if err != nil {
			return err
		}
	}

	var out []byte
	failed := false
	switch dir {
	case "to-deck", "to-dbless":
		var issues []convert.ConversionIssue
		out, issues, err = convertToYAML(in, convert.Options{LabelTagPrefix: *tagPrefix}, dir == "to-dbless")
		if err != nil {
			return err
		}
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, issue)
			failed = failed || *strict || issue.Severity == convert.Error
		}
	case "from-deck":
		var warnings []string
		out, warnings, err = revert.Revert(in, revert.Options{
			Strict:         *strict,
			LabelTagPrefix: *tagPrefix,
		})
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
	default:
		return fmt.Errorf("invalid -direction %q (want auto, to-deck, to-dbless, or from-deck)", dir)
	}

	if err := writeOutput(*outPath, out); err != nil {
		return err
	}
	if failed {
		return errIssues
	}
	return nil
}

func writeOutput(path string, out []byte) error {
	if path == "" || path == "-" {
		_, err := os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(path, out, 0o644) //nolint:gosec,mnd
}

func convertToYAML(in []byte, opts convert.Options, dbless bool) ([]byte, []convert.ConversionIssue, error) {
	result, err := convert.Convert(in, opts)
	if err != nil {
		return nil, nil, err
	}
	if dbless {
		out, err := result.Output.ToDBLess().ToYAML()
		return out, result.Issues, err
	}
	out, err := result.Output.ToYAML()
	return out, result.Issues, err
}

// detectDirection inspects the input document: a decK config carries
// _format_version, an AI Gateway entity-model document does not.
func detectDirection(in []byte) (string, error) {
	var probe struct {
		FormatVersion string `yaml:"_format_version"`
	}
	if err := yaml.Unmarshal(in, &probe); err != nil {
		return "", fmt.Errorf("parsing input document: %w", err)
	}
	if probe.FormatVersion != "" {
		return "from-deck", nil
	}
	return "to-deck", nil
}

func readInput(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
