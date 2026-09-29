package convert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
)

func TestPathParamCapturedAllSyntaxes(t *testing.T) {
	for _, path := range []string{
		"~/openai/(?<model>[^/]+)",
		"~/openai/(?P<model>[^/]+)",
		"~/openai/(?'model'[^/]+)",
	} {
		require.Truef(t, pathParamCaptured([]string{path}, "model", false), "path %q should be detected", path)
	}

	// A capture whose name does not match, a non-regex path, and a path with no
	// capture at all are all rejected.
	require.False(t, pathParamCaptured([]string{"~/openai/(?<other>[^/]+)"}, "model", false))
	require.False(t, pathParamCaptured([]string{"/openai/(?<model>[^/]+)"}, "model", false))
	require.False(t, pathParamCaptured([]string{"~/openai/[^/]+"}, "model", false))

	// Every path in the set must carry the capture.
	require.False(t, pathParamCaptured(
		[]string{"~/openai/(?<model>[^/]+)", "~/alt/(?<other>[^/]+)"}, "model", false))
}

func TestTryConvertPCREToLuaAllSyntaxes(t *testing.T) {
	for _, in := range []string{
		"~/openai/(?<m>[^:/]+)",
		"~/openai/(?P<m>[^:/]+)",
		"~/openai/(?'m'[^:/]+)",
	} {
		require.Equalf(t, "/openai/([%w%.%-:]+)", tryConvertPCREToLua(in), "input %q", in)
	}
	// No named capture falls back to the format default.
	require.Equal(t, aimap.OpenAIDefaultPathPattern, tryConvertPCREToLua("~/openai/[^/]+"))
}

const passthroughProviders = `
model_providers:
  - name: p
    type: openai
    config:
      auth:
        type: basic
        headers:
          - name: Authorization
            value: token
  - name: d
    type: databricks
    config:
      auth:
        type: basic
        headers:
          - name: Authorization
            value: token
`

// TestPassthroughRejects covers the passthrough shapes ai-proxy-advanced cannot serve, which the
// converter must reject rather than lower into a configuration the data plane throws away whole.
func TestPassthroughRejects(t *testing.T) {
	for name, tc := range map[string]struct{ model, want string }{
		"combined with another format": {
			`
  - name: multi
    formats: [{type: openai}, {type: passthrough}]
    targets: [{name: a, provider: p, config: {type: openai}}]`,
			"cannot be combined with other formats",
		},
		// Each type "model" model owns its own ai-proxy-advanced, told apart by the
		// ai-model FK the selector activates. A passthrough model has neither, so two of
		// them on one base path would be two plugins of the same name on one route.
		"two passthrough models on one base path": {
			`
  - name: first
    formats: [{type: passthrough}]
    targets: [{name: a, provider: p, config: {type: openai}}]
  - name: second
    type: api
    formats: [{type: passthrough}]
    targets: [{name: b, provider: p, config: {type: openai}}]`,
			"a passthrough model cannot share route path",
		},
		// Different auth strategies split the route group, but both routes still
		// match the same requests.
		"two passthrough models on one base path with different auth": {
			`
  - name: a
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai]}}
    targets: [{name: t, provider: p, config: {type: openai}}]
  - name: b
    formats: [{type: passthrough}]
    access: {auth_strategies: [key]}
    config: {route: {paths: [/ai]}}
    targets: [{name: t2, provider: p, config: {type: openai}}]`,
			"a passthrough model cannot share route path",
		},
		// The body is forwarded unchanged, so an openai body cannot reach anthropic.
		"targets with different client formats": {
			`
  - name: mixed
    formats: [{type: passthrough}]
    targets:
      - {name: a, provider: p, config: {type: openai}}
      - {name: b, provider: p, config: {type: anthropic}}`,
			"every target must speak openai, not anthropic",
		},
		"semantic balancer": {
			`
  - name: sem
    formats: [{type: passthrough}]
    config: {balancer: {algorithm: semantic}}
    targets: [{name: a, provider: p, config: {type: openai}}]`,
			"cannot use the semantic balancer algorithm",
		},
		"databricks without upstream_url": {
			`
  - name: db
    formats: [{type: passthrough}]
    targets: [{name: a, provider: d, config: {type: databricks, workspace_instance_id: w}}]`,
			"requires upstream_url for databricks",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := Convert([]byte("models:"+tc.model+passthroughProviders), Options{})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// TestPassthroughIgnoresCapabilities pins that a passthrough model serves one route on its bare
// base path, on every method, whatever capabilities it declares -- none, or ones (video) that
// would otherwise add routes of their own.
func TestPassthroughIgnoresCapabilities(t *testing.T) {
	for _, caps := range []string{"[]", "[generate, video, realtime]"} {
		out, warnings, err := Convert([]byte(`
models:
  - name: pt
    capabilities: `+caps+`
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai]}}
    targets: [{name: a, provider: p, config: {type: openai}}]
`+passthroughProviders), Options{})
		require.NoError(t, err, caps)
		require.Empty(t, warnings, caps)
		doc := string(out)
		require.Contains(t, doc, "name: openai-passthrough\n        paths:\n          - /ai\n        strip_path: false", caps)
		require.Equal(t, 1, strings.Count(doc, "name: ai-proxy-advanced"), caps)
		require.NotContains(t, doc, "lifecycle", caps)
	}
}

// TestPassthroughIsRouteScopedAndWarnsAboutPromptReadingPolicies pins the shape a passthrough
// model lowers to: no ai-model-selector and no ai-model FK anywhere, since nothing can set
// ctx.ai_model for a body the selector cannot read, plus a warning for each policy -- the
// model's own or a global one -- that needs the normalized LLM shape the model no longer produces.
func TestPassthroughIsRouteScopedAndWarnsAboutPromptReadingPolicies(t *testing.T) {
	out, warnings, err := Convert([]byte(`
models:
  - name: pt
    type: model
    capabilities: [generate]
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai]}}
    policies: [guard, transform, logs, cache]
    targets:
      - name: gpt-a
        provider: p
        config: {type: openai}
policies:
  - {name: guard, type: ai-prompt-guard, config: {allow_patterns: ["^safe"]}}
  - {name: transform, type: ai-request-transformer, config: {prompt: rewrite}}
  - {name: logs, type: http-log, config: {http_endpoint: "https://logs.internal/x"}}
  - {name: cache, type: ai-semantic-cache, config: {}}
  - {name: lakera, type: ai-lakera-guard, global: true, config: {}}
`+passthroughProviders), Options{})
	require.NoError(t, err)

	doc := string(out)
	require.NotContains(t, doc, "ai-model-selector",
		"the selector reads a fixed request shape; a passthrough body has none")
	require.NotContains(t, doc, "model_alias",
		"ai-proxy-advanced refuses model_alias alongside the passthrough route_type")
	require.NotContains(t, doc, "model: pt", "nothing sets ctx.ai_model, so no plugin may be model-scoped")
	require.Contains(t, doc, "route_type: passthrough")
	require.Contains(t, doc, "name: pt", "the ai_models row is the model identity and stays")

	// Only the policies that read the normalized shape are reported: one that works on raw
	// bytes and one that never looks at the body are both fine as they are.
	require.Len(t, warnings, 3)
	require.Contains(t, warnings[0], `policy "guard" (ai-prompt-guard) may not work properly`)
	require.Contains(t, warnings[1], `policy "cache" (ai-semantic-cache) may not work properly`)
	require.Contains(t, warnings[2], `policy "lakera" (ai-lakera-guard) may not work properly`)
}

// TestPassthroughWarnsAboutSanitizerOnlyWhenItAnonymizesCredentials pins that ai-sanitizer is
// reported only when the anonymize list the data plane ends up with includes credentials.
func TestPassthroughWarnsAboutSanitizerOnlyWhenItAnonymizesCredentials(t *testing.T) {
	for anonymize, warns := range map[string]bool{
		"":                                      true, // the data plane defaults to all_and_credentials
		"anonymize: null":                       true,
		"anonymize: [all_and_credentials]":      true,
		"anonymize: [phone, credentials]":       true,
		"anonymize: [phone, email]":             false,
		"anonymize: [all]":                      false,
		"anonymize: [all, all_and_credentials]": true,
		"anonymize: [all, credentials]":         true,
	} {
		_, warnings, err := Convert([]byte(`
models:
  - name: pt
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai]}}
    policies: [san]
    targets: [{name: t, provider: p, config: {type: openai}}]
policies:
  - name: san
    type: ai-sanitizer
    config: {`+anonymize+`}
`+passthroughProviders), Options{})
		require.NoError(t, err, anonymize)
		if warns {
			require.Len(t, warnings, 1, anonymize)
			require.Contains(t, warnings[0], `policy "san" (ai-sanitizer) may not work properly`, anonymize)
		} else {
			require.Empty(t, warnings, anonymize)
		}
	}
}

// TestPassthroughWarnsAboutProvidersWithoutNativeFormat pins that a passthrough target whose
// provider has no llm_format of its own is reported, since usage extraction may find nothing.
func TestPassthroughWarnsAboutProvidersWithoutNativeFormat(t *testing.T) {
	for providerType, warns := range map[string]bool{
		"openai":      false,
		"anthropic":   false,
		"bedrock":     false,
		"cohere":      false,
		"gemini":      false,
		"vertex":      false,
		"huggingface": false,
		"azure":       true,
		"mistral":     true,
		"sagemaker":   true,
	} {
		_, warnings, err := Convert([]byte(`
models:
  - name: pt
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai]}}
    targets: [{name: t, provider: p, config: {type: `+providerType+`}}]
`+passthroughProviders), Options{})
		require.NoError(t, err, providerType)
		if warns {
			require.Len(t, warnings, 1, providerType)
			require.Contains(t, warnings[0], `provider type "`+providerType+`" has no native llm_format`)
		} else {
			require.Empty(t, warnings, providerType)
		}
	}

	_, _, err := Convert([]byte(`
models:
  - name: pt
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai]}}
    targets: [{name: t, provider: p, config: {type: azure}}]
`+passthroughProviders), Options{Strict: true})
	require.ErrorContains(t, err, "has no native llm_format")
}

// TestPassthroughAllowsHostDisambiguatedModels pins that two passthrough models on one base
// path convert when their hosts tell them apart, and that gemini and vertex targets mix.
func TestPassthroughAllowsHostDisambiguatedModels(t *testing.T) {
	_, _, err := Convert([]byte(`models:
  - name: a
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai], hosts: [a.example]}}
    targets: [{name: t, provider: p, config: {type: openai}}]
  - name: b
    formats: [{type: passthrough}]
    config: {route: {paths: [/ai], hosts: [b.example]}}
    targets:
      - {name: g, provider: p, config: {type: gemini}}
      - {name: v, provider: p, config: {type: vertex}}
`+passthroughProviders), Options{})
	require.NoError(t, err)
}
