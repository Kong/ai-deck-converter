package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

func TestSanitizeInstanceNameSegment(t *testing.T) {
	for in, want := range map[string]string{
		"@openai/gpt-4o-mini": "openai-gpt-4o-mini",
		"gemini-2.5-flash":    "gemini-2-5-flash",
		"a b//c~d_e":          "a-b-c~d_e",
		"--x--":               "x",
		"@":                   "",
	} {
		require.Equal(t, want, sanitizeInstanceNameSegment(in), in)
	}
}

// TestBuildInstanceNameKeepsSegmentsApart checks that a dot in a qualifier
// does not make two different scopes share one name.
func TestBuildInstanceNameKeepsSegmentsApart(t *testing.T) {
	require.NotEqual(t,
		buildInstanceName("p", "a.b", "c"),
		buildInstanceName("p", "a", "b.c"))
	require.Equal(t, "p.x", buildInstanceName("p", "", "x", "@"))
}

func TestPluginInstanceNamesCollision(t *testing.T) {
	src := []byte(`
model_providers:
  - name: p
    type: openai
models:
  - name: a
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config: {route: {paths: [/ai], model: {values: [shared]}}}
    targets: [{name: gpt-5, provider: p}]
  - name: b
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config: {route: {paths: [/ai], model: {values: [shared]}}}
    targets: [{name: gpt-4o, provider: p}]
`)
	opts := testOptions
	opts.PluginInstanceNames = true
	result, err := Convert(src, opts)
	require.NoError(t, err)

	var names []string
	for _, p := range result.Output.Plugins {
		if p.Name == "ai-proxy-advanced" {
			names = append(names, p.InstanceName)
		}
	}
	require.Equal(t, []string{"ai-proxy-advanced.shared.openai-chat", ""}, names)

	var dropped []ConversionIssue
	for _, issue := range result.Issues {
		if issue.Code == ConfigFieldDropped {
			dropped = append(dropped, issue)
		}
	}
	require.Len(t, dropped, 1)
	require.Equal(t, Warning, dropped[0].Severity)
	require.Equal(t, ConversionIssueSource{Kind: kindModel, Name: "b"}, dropped[0].Source)
}

func TestPluginInstanceNamesConsumerCustomID(t *testing.T) {
	src := []byte(`
policies:
  - type: http-log
    name: audit-logging
    config: {http_endpoint: https://logs.internal/ingest}
consumers:
  - custom_id: dev-001
    policies: [audit-logging]
  - custom_id: dev-002
    policies: [audit-logging]
`)
	opts := testOptions
	opts.PluginInstanceNames = true
	result, err := Convert(src, opts)
	require.NoError(t, err)

	var names []string
	for _, consumer := range result.Output.Consumers {
		for _, p := range consumer.Plugins {
			names = append(names, p.InstanceName)
		}
	}
	require.Equal(t, []string{"http-log.dev-001", "http-log.dev-002"}, names)
	for _, issue := range result.Issues {
		require.NotEqual(t, ConfigFieldDropped, issue.Code, issue.Reason)
	}
}

func TestInstanceNameIssueSource(t *testing.T) {
	cases := []struct {
		source *kong.Source
		want   ConversionIssueSource
	}{
		{nil, ConversionIssueSource{Kind: kindConversionOption, Name: "plugin_instance_names"}},
		{
			&kong.Source{EntityType: "identity_provider", EntityName: "idp"},
			ConversionIssueSource{Kind: kindAuthStrategy, Name: "idp"},
		},
		{
			&kong.Source{EntityType: kindModel, EntityName: "m"},
			ConversionIssueSource{Kind: kindModel, Name: "m"},
		},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, instanceNameIssueSource(&kong.Plugin{Source: tc.source}))
	}
}

func TestPluginInstanceNamesOffByDefault(t *testing.T) {
	result, err := Convert([]byte(multiAliasSrc), testOptions)
	require.NoError(t, err)
	for _, p := range result.Output.Plugins {
		require.Empty(t, p.InstanceName, p.Name)
	}
}
