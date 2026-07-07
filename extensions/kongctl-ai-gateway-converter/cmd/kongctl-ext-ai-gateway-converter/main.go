// Command kongctl-ext-ai-gateway-converter converts AI Gateway configuration
// between Kong Gateway decK and kongctl declarative formats.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Kong/ai-deck-converter/extensions/kongctl-ai-gateway-converter/kongctlconvert"
)

const helpText = `Convert AI Gateway configuration between decK and kongctl formats.

Usage:
  kongctl convert ai-gateway <file> --from deck|kongctl --to kongctl|deck --gateway-name NAME [flags]

Arguments:
  file                         Input YAML file. Use - or omit to read from stdin.

Flags:
      --from string            Source format: deck or kongctl.
      --to string              Target format: kongctl or deck.
      --gateway-name string    AI Gateway name to create or select.
      --gateway-display-name   Optional display_name for deck to kongctl output.
      --output-file string     Write converted YAML to this file instead of stdout.
      --strict                 Treat conversion warnings as errors.
      --label-tag-prefix       Prefix for label-derived tags.

Examples:
  kongctl convert ai-gateway deck.yaml --from deck --to kongctl --gateway-name support-ai
  kongctl convert ai-gateway aigw.yaml --from kongctl --to deck --gateway-name support-ai
`

type cliOptions struct {
	convert kongctlconvert.Options
	input   string
	output  string
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if shouldShowHelp(args) {
		_, err := fmt.Fprint(stdout, helpText)
		return err
	}

	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	in, err := readInput(opts.input, stdin)
	if err != nil {
		return err
	}

	out, warnings, err := kongctlconvert.Convert(in, opts.convert)
	if err != nil {
		for _, warning := range warnings {
			fmt.Fprintln(stderr, "warning:", warning)
		}
		return err
	}
	for _, warning := range warnings {
		fmt.Fprintln(stderr, "warning:", warning)
	}

	if opts.output == "" || opts.output == "-" {
		_, err = stdout.Write(out)
		return err
	}
	return os.WriteFile(opts.output, out, 0o644)
}

func parseArgs(args []string) (cliOptions, error) {
	var opts cliOptions
	var err error
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "" {
			continue
		}
		if !strings.HasPrefix(token, "-") || token == "-" {
			if opts.input != "" {
				return cliOptions{}, fmt.Errorf("too many input files: %q and %q", opts.input, token)
			}
			opts.input = token
			continue
		}

		name, value, hasValue := strings.Cut(strings.TrimPrefix(token, "--"), "=")
		switch name {
		case "from":
			value, i, err = nextFlagValue(args, i, name, value, hasValue)
			if err != nil {
				return cliOptions{}, err
			}
			opts.convert.From = value
		case "to":
			value, i, err = nextFlagValue(args, i, name, value, hasValue)
			if err != nil {
				return cliOptions{}, err
			}
			opts.convert.To = value
		case "gateway-name":
			value, i, err = nextFlagValue(args, i, name, value, hasValue)
			if err != nil {
				return cliOptions{}, err
			}
			opts.convert.GatewayName = value
		case "gateway-display-name":
			value, i, err = nextFlagValue(args, i, name, value, hasValue)
			if err != nil {
				return cliOptions{}, err
			}
			opts.convert.GatewayDisplayName = value
		case "output-file":
			value, i, err = nextFlagValue(args, i, name, value, hasValue)
			if err != nil {
				return cliOptions{}, err
			}
			opts.output = value
		case "label-tag-prefix":
			value, i, err = nextFlagValue(args, i, name, value, hasValue)
			if err != nil {
				return cliOptions{}, err
			}
			opts.convert.LabelTagPrefix = value
		case "strict":
			if hasValue {
				switch value {
				case "true":
					opts.convert.Strict = true
				case "false":
					opts.convert.Strict = false
				default:
					return cliOptions{}, fmt.Errorf("flag --strict requires true or false")
				}
			} else {
				opts.convert.Strict = true
			}
		default:
			return cliOptions{}, fmt.Errorf("unknown flag --%s", name)
		}
	}
	return opts, nil
}

func nextFlagValue(args []string, index int, name, value string, hasValue bool) (string, int, error) {
	if hasValue {
		return value, index, nil
	}
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("flag --%s requires a value", name)
	}
	return args[next], next, nil
}

func readInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

func shouldShowHelp(args []string) bool {
	if len(args) == 0 {
		return true
	}
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}
