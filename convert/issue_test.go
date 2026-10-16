package convert

import (
	"errors"
	"strings"
	"sync"
	"testing"

	publicaigw "github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// convertDoc converts src and returns the reason of each issue. It returns an
// error when an issue has the Error severity. With strict, every issue gives
// an error.
func convertDoc(src []byte, strict bool) (*kong.Document, []string, error) {
	result, err := Convert(src, testOptions)
	if err != nil {
		return nil, nil, err
	}
	return result.Output, reasons(result.Issues), issuesError(result.Issues, strict)
}

// convertYAML is convertDoc with the output as decK YAML.
func convertYAML(src []byte) ([]byte, []string, error) {
	doc, warnings, err := convertDoc(src, false)
	if err != nil {
		return nil, warnings, err
	}
	data, err := doc.ToYAML()
	return data, warnings, err
}

func reasons(issues []ConversionIssue) []string {
	var out []string
	for _, issue := range issues {
		out = append(out, issue.Reason)
	}
	return out
}

func issuesError(issues []ConversionIssue, strict bool) error {
	var failed []string
	for _, issue := range issues {
		if strict || issue.Severity == Error {
			failed = append(failed, issue.Reason)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return errors.New(strings.Join(failed, "\n"))
}

// requireIssue asserts that issues hold exactly one issue with code for the
// entity kind/name, and returns it.
func requireIssue(
	t *testing.T, issues []ConversionIssue, severity ConversionIssueSeverity, code ConversionIssueCode,
	kind, name string, fields ...string,
) ConversionIssue {
	t.Helper()
	var found []ConversionIssue
	for _, issue := range issues {
		if issue.Code == code && issue.Source.Kind == kind && issue.Source.Name == name {
			found = append(found, issue)
		}
	}
	require.Len(t, found, 1, "issues: %v", issues)
	require.Equal(t, severity, found[0].Severity, "severity of %v", found[0])
	if len(fields) == 0 {
		fields = nil
	}
	require.Equal(t, fields, found[0].Source.Fields, "fields of %v", found[0])
	return found[0]
}

// A panic in one entity gives an InternalError issue. The other entities
// still convert.
func TestConvertPanicInEntityGivesInternalError(t *testing.T) {
	src := []byte(`
model_providers:
  - name: openai-main
    type: openai
models:
  - name: healthy
    capabilities: [generate]
    targets: [{name: gpt-4o, provider: openai-main, config: {type: openai}}]
    config: {route: {paths: [/healthy]}}
  - name: crashing
    capabilities: [generate]
    targets: [{name: gpt-4o, provider: openai-main, config: {type: openai}}]
    config: {route: {paths: [/healthy]}}
agents:
  - name: crashing-agent
    type: http
    config: {url: https://agent.internal, route: {paths: [/crashing-agent]}}
  - name: healthy-agent
    type: http
    config: {url: https://agent.internal, route: {paths: [/healthy-agent]}}
`)
	doc, err := publicaigw.Parse(src)
	require.NoError(t, err)
	c := newConverter(doc, Options{})
	c.beforeEntity = func(src ConversionIssueSource) {
		if strings.HasPrefix(src.Name, "crashing") {
			panic("boom")
		}
	}
	c.run()
	result := c.result()

	require.Len(t, result.Issues, 2)
	for _, issue := range []ConversionIssue{
		requireIssue(t, result.Issues, Error, InternalError, kindModel, "crashing"),
		requireIssue(t, result.Issues, Error, InternalError, kindAgent, "crashing-agent"),
	} {
		require.Contains(t, issue.Reason, "converter panic: boom")
	}

	require.Len(t, result.Output.AIModels, 1)
	require.Equal(t, "healthy", result.Output.AIModels[0].Name)
	var services []string
	for _, svc := range result.Output.Services {
		services = append(services, svc.Name)
	}
	require.Equal(t, []string{"ai-gateway", "healthy-agent"}, services)
	require.Len(t, result.Output.Services[0].Routes, 1)
	targets := result.Output.Plugins[1].Config["targets"].([]map[string]any)
	require.Len(t, targets, 1, "the crashing model adds no target to the shared route")
}

// oneLine keeps a ConversionIssue printable as one stderr line even when its
// reason embeds a stack trace.
func TestOneLineCollapsesStackTrace(t *testing.T) {
	stack := []byte("goroutine 1 [running]:\nmain.foo()\n\t/path/to/file.go:10 +0x1\n" +
		"main.bar()\n\t/path/to/file.go:20 +0x2\n")
	got := oneLine(stack)
	require.NotContains(t, got, "\n")
	require.Contains(t, got, "main.foo()")
	require.Contains(t, got, "file.go:10")
}

// A panic in a global policy, a custom policy, or an SNI is isolated like a
// panic in any other entity: the other entities still convert, and the
// reported reason stays one line.
func TestConvertPanicInPolicyCustomPolicySNIGivesInternalError(t *testing.T) {
	src := []byte(`
policies:
  - name: crashing-policy
    type: http-log
    global: true
    config: {http_endpoint: https://logs.example.com}
  - name: healthy-policy
    type: http-log
    global: true
    config: {http_endpoint: https://logs.example.com}
custom_policies:
  - name: crashing-custom
    type: streaming
    schema: "return {}"
    handler: "return {}"
  - name: healthy-custom
    type: streaming
    schema: "return {}"
    handler: "return {}"
certificates:
  - name: cert1
    cert: c
    key: k
snis:
  - name: crashing-sni
    hostname: crashing.example.com
    certificate: cert1
  - name: healthy-sni
    hostname: healthy.example.com
    certificate: cert1
`)
	doc, err := publicaigw.Parse(src)
	require.NoError(t, err)
	c := newConverter(doc, Options{})
	c.beforeEntity = func(src ConversionIssueSource) {
		if strings.HasPrefix(src.Name, "crashing") {
			panic("boom")
		}
	}
	c.run()
	result := c.result()

	for _, want := range []struct{ kind, name string }{
		{kindPolicy, "crashing-policy"},
		{kindCustomPolicy, "crashing-custom"},
		{kindSNI, "crashing-sni"},
	} {
		issue := requireIssue(t, result.Issues, Error, InternalError, want.kind, want.name)
		require.Contains(t, issue.Reason, "converter panic: boom")
		require.NotContains(t, issue.Reason, "\n", "the reason must stay one line")
	}

	require.Len(t, result.Output.Plugins, 1)
	require.Equal(t, "healthy-policy", result.Output.Plugins[0].Source.EntityName)
	require.Len(t, result.Output.CustomPlugins, 1)
	require.Equal(t, "healthy-custom", result.Output.CustomPlugins[0].Name)
	require.Len(t, result.Output.Certificates[0].SNIs, 1)
	require.Equal(t, "healthy.example.com", result.Output.Certificates[0].SNIs[0].Name)
}

// Concurrent conversions keep their issues apart.
func TestConvertConcurrentRunsAreIndependent(t *testing.T) {
	bad := []byte("consumers:\n  - name: c1\n    policies: [missing]\n")
	good := []byte("consumers:\n  - name: c2\n")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src, want := good, 0
			if i%2 == 0 {
				src, want = bad, 1
			}
			result, err := Convert(src, testOptions)
			assert.NoError(t, err)
			assert.Len(t, result.Issues, want)
		}()
	}
	wg.Wait()
}
