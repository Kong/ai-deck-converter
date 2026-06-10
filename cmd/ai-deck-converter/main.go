// Command ai-deck-converter translates between an AI Gateway entity-model YAML
// config and a Kong Gateway decK declarative YAML config, in either direction.
// The direction is auto-detected from the input (a decK document carries
// _format_version) and can be forced with -direction.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/gperanich/ai-deck-converter/convert"
	"github.com/gperanich/ai-deck-converter/revert"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		outPath   = flag.String("o", "", "output file path (default: stdout)")
		strict    = flag.Bool("strict", false, "treat unresolved references and unconvertible entities as errors instead of warnings")
		tagPrefix = flag.String("label-tag-prefix", "", "prefix for label-derived tags (prepended when converting, stripped when reverting), e.g. aigw/")
		direction = flag.String("direction", "auto", "conversion direction: auto, to-deck (AI Gateway -> decK), to-dbless (AI Gateway -> db-less), or from-deck (decK -> AI Gateway)")
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
	var warnings []string
	switch dir {
	case "to-deck":
		out, warnings, err = convert.Convert(in, convert.Options{
			Strict:         *strict,
			LabelTagPrefix: *tagPrefix,
			OutputMode:     "deck",
		})
	case "to-dbless":
		out, warnings, err = convert.Convert(in, convert.Options{
			Strict:         *strict,
			LabelTagPrefix: *tagPrefix,
			OutputMode:     "db-less",
		})
	case "from-deck":
		out, warnings, err = revert.Revert(in, revert.Options{
			Strict:         *strict,
			LabelTagPrefix: *tagPrefix,
		})
	default:
		return fmt.Errorf("invalid -direction %q (want auto, to-deck, to-dbless, or from-deck)", dir)
	}
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	if *outPath == "" || *outPath == "-" {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(*outPath, out, 0o644)
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
