package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunConvertsStdinToKongctl(t *testing.T) {
	input := `
_format_version: "3.0"
services: []
`
	var stdout, stderr bytes.Buffer

	err := run([]string{
		"--from", "deck",
		"--to", "kongctl",
		"--gateway-name", "support-ai",
	}, bytes.NewBufferString(input), &stdout, &stderr)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "ai_gateways:")
	require.Contains(t, stdout.String(), "name: support-ai")
	require.Empty(t, stderr.String())
}

func TestRunWritesOutputFile(t *testing.T) {
	input := filepath.Join(t.TempDir(), "deck.yaml")
	output := filepath.Join(t.TempDir(), "aigw.yaml")
	require.NoError(t, os.WriteFile(input, []byte(`_format_version: "3.0"`), 0o644))

	var stdout, stderr bytes.Buffer
	err := run([]string{
		input,
		"--from=deck",
		"--to=kongctl",
		"--gateway-name=support-ai",
		"--output-file", output,
	}, bytes.NewBuffer(nil), &stdout, &stderr)

	require.NoError(t, err)
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Contains(t, string(data), "ai_gateways:")
}

func TestRunRequiresDirectionAndGateway(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--from", "deck", "--to", "kongctl"}, bytes.NewBufferString(""), &stdout, &stderr)
	require.ErrorContains(t, err, "--gateway-name is required")
}

func TestRunIgnoresKongctlOutputContext(t *testing.T) {
	contextPath := filepath.Join(t.TempDir(), "context.json")
	require.NoError(t, os.WriteFile(contextPath, []byte(`{
  "resolved": {"output": "yaml"},
  "output": {"format": "yaml"}
}`), 0o600))
	t.Setenv("KONGCTL_EXTENSION_CONTEXT", contextPath)

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"--from", "deck",
		"--to", "kongctl",
		"--gateway-name", "support-ai",
	}, bytes.NewBufferString(`_format_version: "3.0"`), &stdout, &stderr)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "ai_gateways:")
	require.Empty(t, stderr.String())
}

func TestRunShowsHelpWithNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(nil, bytes.NewBufferString(""), &stdout, &stderr)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "Usage:")
	require.Contains(t, stdout.String(), "kongctl convert ai-gateway <file>")
	require.Empty(t, stderr.String())
}

func TestParseArgsAllowsFlagsAfterInput(t *testing.T) {
	opts, err := parseArgs([]string{
		"deck.yaml",
		"--from", "deck",
		"--to", "kongctl",
		"--gateway-name", "support-ai",
		"--strict",
	})

	require.NoError(t, err)
	require.Equal(t, "deck.yaml", opts.input)
	require.Equal(t, "deck", opts.convert.From)
	require.Equal(t, "kongctl", opts.convert.To)
	require.Equal(t, "support-ai", opts.convert.GatewayName)
	require.True(t, opts.convert.Strict)
}
