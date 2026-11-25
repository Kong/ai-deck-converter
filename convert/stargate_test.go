package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKongStargateConversionOptions(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/153_kong_stargate/input.yaml")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, region, target, runtime, issue, option string
	}{
		{
			name: "endpoint override", region: "us",
			target: ", upstream_url: 'https://example.com/custom'", runtime: "2.4",
			option: "upstream_url: https://example.com/custom",
		},
		{
			name: "base override", region: "us",
			target: ", upstream_base_url: 'https://example.com/base'", runtime: "2.4",
			option: "upstream_base_url: https://example.com/base",
		},
		{
			name: "invalid region with override", region: "eu",
			target: ", upstream_url: 'https://example.com/custom'", runtime: "2.4",
			issue: "Stargate region must be us",
		},
		{name: "null region", region: "null", runtime: "2.4", issue: "Stargate region must be us"},
		{name: "old runtime", region: "us", runtime: "2.3", issue: "unsupported"},
		{
			name: "two overrides", region: "us",
			target:  ", upstream_url: 'https://example.com/a', upstream_base_url: 'https://example.com/b'",
			runtime: "2.4", issue: "mutually exclusive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.ReplaceAll(string(input), "region: us", "region: "+tc.region)
			src = strings.ReplaceAll(src, "type: kong-stargate}", "type: kong-stargate"+tc.target+"}")
			result, err := Convert([]byte(src), Options{MinRuntimeVersion: tc.runtime})
			require.NoError(t, err)
			output, err := result.Output.ToYAML()
			require.NoError(t, err)
			if tc.issue != "" {
				require.NotEmpty(t, result.Issues)
				if tc.runtime == "2.3" {
					require.Equal(t, UnsupportedInMinRuntimeVersion, result.Issues[0].Code)
				} else {
					require.Contains(t, result.Issues[0].Reason, tc.issue)
				}
				require.NotContains(t, string(output), "provider: kong-stargate")
				return
			}
			require.Empty(t, result.Issues)
			require.Equal(t, 3, strings.Count(string(output), tc.option))
			require.NotContains(t, string(output), "us.inference.konghq.com")
		})
	}
}

func TestInvalidStargateTargetBlocksMixedModel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ suffix, field string }{
		{"allow_auth_override: true, config: {type: kong-stargate}", "allow_auth_override"},
		{
			`config: {type: kong-stargate, upstream_url: 'https://example.com/a', ` +
				`upstream_base_url: 'https://example.com/b'}`,
			"config.upstream_base_url",
		},
		{"config: {type: openai}", "config.type"},
	} {
		src := `
model_providers:
  - {name: openai, type: openai}
  - {name: stargate, type: kong-stargate, config: {region: us}}
models:
  - name: mixed
    formats: [{type: openai}]
    capabilities: [generate]
    targets:
      - {name: gpt-4o, provider: openai}
      - {name: kong/model, provider: stargate, ` + tc.suffix + `}
`
		result, err := Convert([]byte(src), Options{MinRuntimeVersion: "2.4"})
		require.NoError(t, err)
		require.Len(t, result.Issues, 1)
		require.Equal(t, Error, result.Issues[0].Severity)
		require.Equal(t, []string{"targets[1]." + tc.field}, result.Issues[0].Source.Fields)
	}
}

func TestStargateSemanticEmbeddingsRejectAuthInheritance(t *testing.T) {
	t.Parallel()
	for _, embeddings := range []string{
		"provider: openai, model: {name: embeddings, config: {type: kong-stargate}}",
		"provider: stargate, model: {name: embeddings, config: {type: openai}}",
		"provider: openai, model: {name: embeddings, provider: kong-stargate}",
	} {
		src := `
model_providers:
  - {name: openai, type: openai, config: {auth: {type: basic, headers: [{name: Authorization, value: test-key}]}}}
  - {name: stargate, type: kong-stargate, config: {region: us}}
models:
  - name: inference
    formats: [{type: openai}]
    capabilities: [generate]
    config:
      balancer: {algorithm: semantic, embeddings: {` + embeddings + `}}
    targets: [{name: gpt-4o, provider: openai}]
`
		result, err := Convert([]byte(src), Options{MinRuntimeVersion: "2.4"})
		require.NoError(t, err)
		require.Len(t, result.Issues, 1)
		require.Equal(t, Error, result.Issues[0].Severity)
		require.Contains(t, result.Issues[0].Reason, "does not support semantic embeddings")
		output, err := result.Output.ToYAML()
		require.NoError(t, err)
		require.NotContains(t, string(output), "test-key")
	}
}

func TestStargateWithNoUsableEndpointRejectsModel(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("testdata/153_kong_stargate/input.yaml")
	require.NoError(t, err)
	input := strings.ReplaceAll(string(src), `formats: [{type: "*"}]`, "formats: [{type: gemini}]")
	result, err := Convert([]byte(input), Options{MinRuntimeVersion: "2.4"})
	require.NoError(t, err)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Issues[len(result.Issues)-1].Reason, "no usable endpoint")
	require.Empty(t, result.Output.AIModels)
}

func TestStargateWithoutEndpointRejectsModelWithRoutableGeminiTarget(t *testing.T) {
	t.Parallel()

	const input = `
model_providers:
  - {name: gemini, type: gemini}
  - {name: stargate, type: kong-stargate, config: {region: us}}
models:
  - name: mixed
    formats: [{type: gemini}]
    capabilities: ["*"]
    targets:
      - {name: gemini-model, provider: gemini}
      - {name: stargate-model, provider: stargate}
`
	opts := Options{MinRuntimeVersion: "2.4"}
	baseline, err := Convert(
		[]byte(
			strings.ReplaceAll(input, "      - {name: stargate-model, provider: stargate}\n", ""),
		),
		opts,
	)
	require.NoError(t, err)
	require.NotEmpty(t, baseline.Output.AIModels)

	result, err := Convert([]byte(input), opts)
	require.NoError(t, err)
	require.True(t, result.HasErrors())
	issue := result.Issues[len(result.Issues)-1]
	require.Equal(t, []string{"targets[1]"}, issue.Source.Fields)
	require.Contains(t, issue.Reason, "no usable endpoint")
	require.Empty(t, result.Output.AIModels)
}

func TestNonStargateProviderIgnoresRegion(t *testing.T) {
	t.Parallel()

	const input = `
model_providers:
  - name: openai
    type: openai
    config:
      auth: {type: basic, headers: [{name: Authorization, value: test-key}]}
models:
  - name: inference
    formats: [{type: openai}]
    capabilities: [generate]
    targets: [{name: gpt-4o, provider: openai}]
`
	opts := Options{MinRuntimeVersion: "2.4"}
	baseline, err := Convert([]byte(input), opts)
	require.NoError(t, err)
	require.Empty(t, baseline.Issues)
	expected, err := baseline.Output.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(expected), "provider: openai")

	for _, region := range []string{"us", "{label: us}", "[us, eu]"} {
		t.Run(region, func(t *testing.T) {
			src := strings.Replace(input, "      auth:", "      region: "+region+"\n      auth:", 1)
			result, err := Convert([]byte(src), opts)
			require.NoError(t, err)
			require.Empty(t, result.Issues)
			output, err := result.Output.ToYAML()
			require.NoError(t, err)
			require.Equal(t, expected, output)
		})
	}
}
