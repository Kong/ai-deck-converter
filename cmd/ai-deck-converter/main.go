// Command ai-deck-converter translates an AI Gateway entity-model YAML config
// into a Kong Gateway decK declarative YAML config.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gperanich/ai-deck-converter/convert"
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
		strict    = flag.Bool("strict", false, "treat unresolved references as errors instead of warnings")
		tagPrefix = flag.String("label-tag-prefix", "", "prefix prepended to label-derived tags, e.g. aigw/")
	)
	flag.Parse()

	in, err := readInput(flag.Arg(0))
	if err != nil {
		return err
	}

	opts := convert.Options{
		Strict:         *strict,
		LabelTagPrefix: *tagPrefix,
	}
	out, warnings, err := convert.Convert(in, opts)
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

func readInput(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
