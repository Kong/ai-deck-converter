package convert

import (
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
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

const overrideProvider = `
model_providers:
  - name: openai-prod
    type: openai
    config:
      auth: {type: basic, headers: [{name: Authorization, value: x}]}
`

func TestModelWarnsRouteNameOverride(t *testing.T) {
	src := `models:
  - name: m
    capabilities: [generate, embeddings]
    config: {route: {name: custom, paths: [/ai]}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
` + overrideProvider
	_, warnings, err := Convert([]byte(src), Options{})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `sets config.route.name to "custom"`)

	_, _, err = Convert([]byte(src), Options{Strict: true})
	require.Error(t, err)
}

func TestModelWarnsLogStatisticsOverride(t *testing.T) {
	src := `models:
  - name: m
    capabilities: [generate, audio]
    config:
      route: {paths: [/ai]}
      logging: {statistics: true}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
` + overrideProvider
	out, warnings, err := Convert([]byte(src), Options{})
	require.NoError(t, err)
	require.Len(t, warnings, 3, "one warning per affected route")
	for i, route := range []string{"openai-audio-speech", "openai-audio-transcribe", "openai-audio-translate"} {
		require.Contains(t, warnings[i], `ignored on the "`+route+`" route`)
		require.Contains(t, warnings[i], "log_statistics will be overridden to false")
	}
	require.Contains(t, string(out), "log_statistics: true", "chat route keeps the user value")

	_, _, err = Convert([]byte(src), Options{Strict: true})
	require.Error(t, err)
}

func TestModelDoesNotWarnLogStatisticsWhenSupportedOrUnset(t *testing.T) {
	for name, src := range map[string]string{
		"supported": `models:
  - name: m
    capabilities: [generate]
    config: {route: {paths: [/ai]}, logging: {statistics: true}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`,
		"defaulted": `models:
  - name: m
    capabilities: [audio]
    config: {route: {paths: [/ai]}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, warnings, err := Convert([]byte(src+overrideProvider), Options{Strict: true})
			require.NoError(t, err)
			require.Empty(t, warnings)
		})
	}
}

func convertRealtimeDocument(t *testing.T, src string) (*kong.Document, []string) {
	t.Helper()
	input, err := aigw.Parse([]byte(src))
	require.NoError(t, err)
	doc, warnings, err := ConvertDocument(input, Options{})
	require.NoError(t, err)
	return doc, warnings
}

func routeByName(t *testing.T, doc *kong.Document, name string) (kong.Service, kong.Route) {
	t.Helper()
	for _, svc := range doc.Services {
		for _, route := range svc.Routes {
			if route.Name == name {
				return svc, route
			}
		}
	}
	t.Fatalf("route %q not found", name)
	return kong.Service{}, kong.Route{}
}

func pluginsOnRoute(doc *kong.Document, route string) []kong.Plugin {
	var out []kong.Plugin
	for _, p := range doc.Plugins {
		if p.Route != nil && string(*p.Route) == route {
			out = append(out, p)
		}
	}
	return out
}

func TestRealtimeRouteUsesWebSocketTransport(t *testing.T) {
	src := `models:
  - name: m
    capabilities: [generate, realtime]
    config:
      route:
        paths: [/ai]
        methods: [GET, POST]
        protocols: [https, wss]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
` + overrideProvider
	doc, warnings := convertRealtimeDocument(t, src)

	httpService, chat := routeByName(t, doc, "openai-chat")
	require.Equal(t, aimap.GatewayServiceName, httpService.Name)
	require.Equal(t, []string{"GET", "POST"}, chat.Methods)
	require.Equal(t, []string{"https"}, chat.Protocols, "wss maps to its HTTP counterpart")

	wsService, realtime := routeByName(t, doc, "openai-realtime")
	require.Equal(t, aimap.GatewayWebSocketServiceName, wsService.Name)
	require.Equal(t, aimap.GatewayWebSocketServiceURL, wsService.URL)
	require.Nil(t, realtime.Methods)
	require.Equal(t, []string{"wss"}, realtime.Protocols, "https maps to its WebSocket counterpart")

	plugins := pluginsOnRoute(doc, "openai-realtime")
	require.Len(t, plugins, 1, "no ai-model-selector on the WebSocket route")
	require.Equal(t, "ai-proxy-advanced", plugins[0].Name)
	require.Nil(t, plugins[0].Model)
	require.Equal(t, []string{"wss"}, plugins[0].Protocols)

	require.Len(t, warnings, 3)
	require.Contains(t, warnings[0], `overridden to [https] on the "openai-chat" route`)
	require.Contains(t, warnings[1], `ignored on the WebSocket route "openai-realtime"`)
	require.Contains(t, warnings[2], `overridden to [wss] on the "openai-realtime" route`)

	input, err := aigw.Parse([]byte(src))
	require.NoError(t, err)
	_, _, err = ConvertDocument(input, Options{Strict: true})
	require.Error(t, err)
}

func TestRealtimeRoutesAreNotSharedBetweenModels(t *testing.T) {
	doc, warnings := convertRealtimeDocument(t, `models:
  - name: a
    capabilities: [realtime]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
  - name: b
    capabilities: [realtime]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`+overrideProvider)
	require.Empty(t, warnings)
	require.Len(t, doc.Services, 1, "no HTTP Service without HTTP routes")
	require.Equal(t, aimap.GatewayWebSocketServiceName, doc.Services[0].Name)
	require.Len(t, doc.Services[0].Routes, 2)
}

func TestRealtimeRouteAuthStrategyHasNoAnonymousFallback(t *testing.T) {
	const strategies = `
auth_strategies:
  - {name: oidc, type: openid-connect, config: {issuer: https://idp.example.com}}
  - {name: keys, type: key-auth, config: {}}
`
	doc, warnings := convertRealtimeDocument(t, `models:
  - name: m
    capabilities: [realtime]
    access: {auth_strategies: [oidc]}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`+overrideProvider+strategies)
	require.Empty(t, warnings)
	require.Empty(t, doc.Consumers, "request-termination cannot run on WebSocket routes")
	var auth *kong.Plugin
	for _, p := range pluginsOnRoute(doc, "openai-realtime") {
		if p.Name == "openid-connect" {
			auth = &p
		}
	}
	require.NotNil(t, auth)
	require.NotContains(t, auth.Config, "anonymous")
	require.Equal(t, []string{"ws", "wss"}, auth.Protocols)

	_, _, err := Convert([]byte(`models:
  - name: m
    capabilities: [realtime]
    access: {auth_strategies: [oidc, keys]}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`+overrideProvider+strategies), Options{})
	require.ErrorContains(t, err, "supports only one auth strategy")
}

// TestRealtimeRouteRejectsNonWebSocketAuthStrategy pins that a realtime model
// cannot use an auth strategy plugin that rejects the ws/wss protocols (jwt,
// confirmed against Kong AI Gateway 2.0.2, 2.1, and 2.2.0): emitting it there
// would either leave the WebSocket route unprotected (the plugin would never
// run, since its default protocols exclude ws/wss) or make the data plane
// refuse the whole config, so the conversion fails instead.
func TestRealtimeRouteRejectsNonWebSocketAuthStrategy(t *testing.T) {
	_, _, err := Convert([]byte(`models:
  - name: m
    capabilities: [realtime]
    access: {auth_strategies: [legacy]}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`+overrideProvider+`
auth_strategies:
  - {name: legacy, type: jwt, config: {}}
`), Options{})
	require.ErrorContains(t, err, `auth strategy plugin "jwt" does not support the ws and wss protocols`)
}

func TestTransportProtocols(t *testing.T) {
	for _, tc := range []struct {
		in        []string
		websocket bool
		want      []string
	}{
		{nil, false, nil},
		{[]string{"https"}, false, []string{"https"}},
		{[]string{"ws", "wss"}, false, []string{"http", "https"}},
		{[]string{"http", "ws"}, false, []string{"http"}},
		{nil, true, []string{"ws", "wss"}},
		{[]string{"https"}, true, []string{"wss"}},
		{[]string{"grpc"}, true, []string{"ws", "wss"}},
		{[]string{"http", "ws", "wss"}, true, []string{"ws", "wss"}},
	} {
		require.Equal(t, tc.want, transportProtocols(tc.in, tc.websocket), "%v websocket=%v", tc.in, tc.websocket)
	}
}

func TestRealtimeRouteSkipsPluginsWithoutWebSocketSupport(t *testing.T) {
	src := `models:
  - name: m
    capabilities: [generate, realtime]
    policies: [guard]
    access: {acls: {allow: [premium]}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
policies:
  - {type: ai-prompt-guard, name: guard, config: {deny_patterns: [forbidden]}}
` + overrideProvider
	doc, warnings := convertRealtimeDocument(t, src)

	var realtimeNames []string
	for _, p := range pluginsOnRoute(doc, "openai-realtime") {
		realtimeNames = append(realtimeNames, p.Name)
	}
	require.ElementsMatch(t, []string{"ai-proxy-advanced", "acl"}, realtimeNames)

	var chatNames []string
	for _, p := range pluginsOnRoute(doc, "openai-chat") {
		chatNames = append(chatNames, p.Name)
	}
	require.Contains(t, chatNames, "ai-prompt-guard", "the HTTP route keeps the policy")

	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `plugin "ai-prompt-guard", which does not support the ws and wss protocols`)

	input, err := aigw.Parse([]byte(src))
	require.NoError(t, err)
	_, _, err = ConvertDocument(input, Options{Strict: true})
	require.Error(t, err)
}
