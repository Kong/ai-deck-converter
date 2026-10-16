package convert

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConvertWarnsPathParamNotCapturedNonRegex(t *testing.T) {
	src := []byte(`
model_providers:
  - {name: openai-prod, type: openai}
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: openai-prod
        config: {type: openai}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route:
        paths: [/v1/chat/completions]
        model: {path: {path_param: model_name}}
`)
	out, warnings, err := convertYAML(src)
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "is not a regex path",
		"expected non-regex path_param warning")
	// The body-default fallback still produces a config in non-strict mode.
	require.Contains(t, string(out), "source: body")

	_, _, err = convertDoc(src, true)
	require.Error(t, err, "expected -strict to turn the warning into an error")
}

func TestConvertWarnsPathParamCaptureNameMismatch(t *testing.T) {
	src := []byte(`
model_providers:
  - {name: openai-prod, type: openai}
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: openai-prod
        config: {type: openai}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route:
        paths: ['~/openai/(?<other_group>[^:/]+)']
        model: {path: {path_param: model_name}}
`)
	_, warnings, err := convertDoc(src, false)
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "no matching named capture group",
		"expected capture-name-mismatch warning")

	_, _, err = convertDoc(src, true)
	require.Error(t, err, "expected -strict to turn the warning into an error")
}

func TestConvertGeminiPathParamFallbackDoesNotWarn(t *testing.T) {
	// Gemini's default selector still reads the model from the path, so a
	// path_param whose base path carries no capture is a harmless fallback, not
	// the silent path->body switch that warrants a warning.
	src := []byte(`
model_providers:
  - {name: gemini-provider, type: gemini}
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: gemini}]
    targets:
      - name: gemini-2.5-pro
        provider: gemini-provider
        config: {type: gemini}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route:
        paths: [/gemini-path]
        model: {path: {path_param: model_name}}
`)
	_, _, err := convertDoc(src, true)
	require.NoError(t, err, "gemini path_param fallback must not warn even under -strict")
}

func TestConvertUnknownProviderLeavesOutTargets(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
  # The target is left out before the skills passthrough guard runs.
  - type: api
    name: skills-api
    capabilities: [skills]
    formats: [{type: openai}]
    targets:
      - name: gpt-5.6
        provider: missing-provider
    config:
      route: {paths: [/ai]}
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	require.Len(t, result.Issues, 4)
	for _, name := range []string{"m1", "skills-api"} {
		requireIssue(t, result.Issues, Warning, UndefinedReference, kindModel, name, "targets[0].provider")
		requireIssue(t, result.Issues, Error, MissingRequiredConfigField, kindModel, name, "targets")
	}
	require.Empty(t, result.Output.AIModels)
}

func encodeYAML(src []byte, opts Options) ([]byte, []ConversionIssue, error) {
	result, err := Convert(src, opts)
	if err != nil {
		return nil, nil, err
	}
	data, err := result.Output.ToYAML()
	return data, result.Issues, err
}

func encodeDBLessYAML(src []byte, opts Options) ([]byte, []ConversionIssue, error) {
	result, err := Convert(src, opts)
	if err != nil {
		return nil, nil, err
	}
	data, err := result.Output.ToDBLess().ToYAML()
	return data, result.Issues, err
}

func TestConvertMetadataTracksGeneratedTargetSources(t *testing.T) {
	src := []byte(`
model_providers:
  - name: local
    type: ollama
models:
  - type: model
    name: local-llama
    capabilities: [generate, agentic]
    formats: [{type: openai}]
    targets:
      - name: llama3.1:8b
        provider: local
        config: {type: llama2, format: ollama}
    config:
      route: {paths: [/ai]}
      model: {}
`)

	doc, _, err := convertDoc(src, false)
	require.NoError(t, err)
	metadata := doc.ToDBLess().Metadata()
	require.Equal(t, []PluginTargetSource{
		{
			PluginIndex:      1,
			Location:         "plugins[1]",
			TargetIndex:      0,
			ModelName:        "local-llama",
			ModelTargetIndex: 0,
			Capabilities:     []string{"generate"},
			CapabilityLabels: []string{"Chat completions"},
		},
		{
			PluginIndex:      3,
			Location:         "plugins[3]",
			TargetIndex:      0,
			ModelName:        "local-llama",
			ModelTargetIndex: 0,
			Capabilities:     []string{"agentic"},
			CapabilityLabels: []string{"Responses"},
		},
	}, metadata.PluginTargets)
}

func TestConvertMetadataMergesCapabilitiesOnCollapsedTarget(t *testing.T) {
	// Bedrock's invoke endpoint for "generate" is field-for-field identical to
	// "audio/speech"'s endpoint, so a model declaring both collapses onto one
	// bedrock-invoke target (see testdata/58_bedrock_generate_and_speech).
	// The target-fingerprint dedup in convertModels must attribute both
	// capabilities to that shared target instead of silently dropping one.
	src, err := os.ReadFile("testdata/58_bedrock_generate_and_speech/input.yaml")
	require.NoError(t, err)

	doc, warnings, err := convertDoc(src, false)
	require.NoError(t, err)
	metadata := doc.ToDBLess().Metadata()
	require.Empty(t, warnings)

	var invokeTarget *PluginTargetSource
	for i := range metadata.PluginTargets {
		pt := &metadata.PluginTargets[i]
		if pt.Location == "plugins[3]" { // bedrock-invoke route's ai-proxy-advanced plugin
			invokeTarget = pt
		}
	}
	require.NotNil(t, invokeTarget, "expected a plugin target for the bedrock-invoke route")
	require.Equal(t, []string{"generate", "audio/speech"}, invokeTarget.Capabilities,
		"the shared bedrock-invoke target must retain both capabilities that produced it")
	require.Equal(t, []string{"Chat completions", "audio/speech"}, invokeTarget.CapabilityLabels)
}

func TestConvertMetadataTracksMCPGeneratedEntities(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: tools
    config:
      route: {paths: [/mcp]}
    tools:
      - {name: search, description: Search, method: GET, path: /search, scheme: https, host: tools.internal}
`)

	doc, _, err := convertDoc(src, false)
	require.NoError(t, err)
	metadata := doc.ToDBLess().Metadata()

	require.Len(t, metadata.Plugins, 1)
	require.Equal(t, GeneratedEntitySource{
		Index:       0,
		Location:    "plugins[0]",
		EntityType:  "mcp_server",
		EntityName:  "tools",
		FieldPrefix: "config",
		FieldMappings: []FieldMapping{
			{GeneratedPrefix: "config.mode", SourcePrefix: "type"},
			{GeneratedPrefix: "config.tools", SourcePrefix: "tools"},
			{GeneratedPrefix: "config.proxy_config", SourcePrefix: "config.proxy"},
			{GeneratedPrefix: "config.auth", SourcePrefix: "config.upstream.auth"},
			{GeneratedPrefix: "config.auth.token_vault", SourcePrefix: "token_vault"},
			{GeneratedPrefix: "config.default_acl", SourcePrefix: "access"},
			{GeneratedPrefix: "config.acl_attribute_type", SourcePrefix: "access.acl_attribute_type"},
			{GeneratedPrefix: "config.access_token_claim_field", SourcePrefix: "access.access_token_claim_field"},
			{GeneratedPrefix: "config.server.tag", SourcePrefix: "config.server.tag"},
		},
	}, metadata.Plugins[0])
	require.Len(t, metadata.Routes, 1)
	require.Equal(t, "config.route", metadata.Routes[0].FieldPrefix)
	require.Len(t, metadata.Services, 1)
	require.Equal(t, "config.url", metadata.Services[0].FieldPrefix)
	require.Contains(t, metadata.Services[0].FieldMappings, FieldMapping{
		GeneratedPrefix: "protocol",
		SourcePrefix:    "config.url",
	})
}

func TestConvertMetadataTracksNestedDeckEntities(t *testing.T) {
	src := []byte(`
agents:
  - type: a2a
    name: booking-agent
    config:
      url: https://agent.internal
      route: {paths: [/agents/book]}
`)

	doc, _, err := convertDoc(src, false)
	require.NoError(t, err)
	metadata := doc.Metadata()
	require.Len(t, metadata.Services, 1)
	require.Equal(t, "services[0]", metadata.Services[0].Location)
	require.Equal(t, "config.url", metadata.Services[0].FieldPrefix)
	require.Equal(t, []GeneratedEntitySource{{
		Index:       0,
		Location:    "services[0].routes[0]",
		EntityType:  "agent",
		EntityName:  "booking-agent",
		FieldPrefix: "config.route",
	}}, metadata.Routes)
	require.Equal(t, []GeneratedEntitySource{{
		Index:       0,
		Location:    "services[0].routes[0].plugins[0]",
		EntityType:  "agent",
		EntityName:  "booking-agent",
		FieldPrefix: "config",
		FieldMappings: []FieldMapping{
			{GeneratedPrefix: "config.proxy_config", SourcePrefix: "config.proxy"},
			{GeneratedPrefix: "config.auth", SourcePrefix: "config.upstream.auth"},
		},
	}}, metadata.Plugins)
}

func TestConvertWarnsAndEmitsMultiTargetVideoLifecycleRoutes(t *testing.T) {
	src := []byte(`
model_providers:
  - name: primary
    type: openai
  - name: fallback
    type: openai
models:
  - type: model
    name: sora
    capabilities: [video]
    formats: [{type: openai}]
    config:
      route: {paths: [/ai]}
      model: {alias: sora}
    targets:
      - name: sora-2
        provider: primary
        config: {type: openai}
      - name: sora-2-pro
        provider: fallback
        config: {type: openai}
`)

	out, warnings, err := convertYAML(src)
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "multiple targets")

	var deck struct {
		Plugins []struct {
			Name   string `yaml:"name"`
			Route  string `yaml:"route"`
			Model  string `yaml:"model"`
			Config struct {
				Targets []struct {
					Description string `yaml:"description"`
					Model       struct {
						ModelAlias string `yaml:"model_alias"`
					} `yaml:"model"`
				} `yaml:"targets"`
			} `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &deck), "parse generated decK config")
	for _, plugin := range deck.Plugins {
		if plugin.Name != "ai-proxy-advanced" || plugin.Route != "openai-videos-lifecycle" {
			continue
		}
		require.Empty(t, plugin.Model, "lifecycle plugin must not require a model alias")
		require.Len(t, plugin.Config.Targets, 2)
		for _, target := range plugin.Config.Targets {
			require.Empty(t, target.Model.ModelAlias,
				"lifecycle target must use the default balancer pool")
		}
		require.ElementsMatch(t, []string{"sora-2", "sora-2-pro"}, []string{
			plugin.Config.Targets[0].Description,
			plugin.Config.Targets[1].Description,
		})
		return
	}
	t.Fatal("expected route-scoped OpenAI video lifecycle plugin")
}

func TestConvertWarnsAndEmitsSharedVideoLifecycleRoutes(t *testing.T) {
	src := []byte(`
model_providers:
  - name: primary
    type: openai
models:
  - type: model
    name: sora-a
    capabilities: [video]
    formats: [{type: openai}]
    config:
      route: {name: first, paths: [/ai]}
      model: {alias: sora-a}
    targets:
      - name: sora-2
        provider: primary
        config: {type: openai}
  - type: model
    name: sora-b
    capabilities: [video]
    formats: [{type: openai}]
    config:
      route: {name: second, paths: [/ai]}
      model: {alias: sora-b}
    targets:
      - name: sora-2-pro
        provider: primary
        config: {type: openai}
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	var lifecycleModels []string
	for _, issue := range result.Issues {
		// The lifecycle check already reports the lifecycle routes, so the
		// route check reports only the creation routes.
		require.NotContains(t, issue.Reason, `route "openai-videos-lifecycle`)
		if strings.Contains(issue.Reason, "shared by multiple video models") {
			require.Equal(t, RoutingConflict, issue.Code)
			require.Equal(t, Warning, issue.Severity)
			require.Equal(t, []string{"config.route"}, issue.Source.Fields)
			lifecycleModels = append(lifecycleModels, issue.Source.Name)
		}
	}
	require.Equal(t, []string{"sora-a", "sora-b"}, lifecycleModels)
	issue := requireIssue(t, slices.DeleteFunc(slices.Clone(result.Issues), func(i ConversionIssue) bool {
		return strings.Contains(i.Reason, "shared by multiple video models")
	}), Warning, RoutingConflict, kindModel, "sora-b", "config.route")
	require.Contains(t, issue.Reason, `route "openai-videos-2"`)
	out, err := result.Output.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(out), "openai-videos-lifecycle")
	require.Contains(t, string(out), "openai-videos-lifecycle-2")
}

// A single model whose capabilities all resolve to the same route (e.g. a
// Bedrock model exposing embeddings+image+video, all served by /invoke) would
// otherwise collapse into one ai-proxy-advanced carrying one genai_category but
// targets with mismatched route_types — a config the data plane rejects. The
// converter keeps the first capability and drops the rest with a warning.
func TestConvertGuardsMultimodalCollapse(t *testing.T) {
	src := []byte(`
model_providers:
  - name: bedrock-prod
    type: bedrock
models:
  - type: model
    name: nova-omni
    capabilities: [embeddings, image, video]
    formats: [{type: bedrock}]
    targets:
      - name: amazon.nova-omni-v1:0
        provider: bedrock-prod
        config: {type: bedrock, region: us-east-1}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route:
        paths: [/ai]
        model:
          values: ["@bedrock/nova-omni"]
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	var dropped []ConversionIssue
	for _, issue := range result.Issues {
		if issue.Code == ConfigFieldDropped {
			dropped = append(dropped, issue)
		}
	}
	require.Len(t, dropped, 2, "issues: %v", result.Issues)
	for i, capability := range []string{"image", "video"} {
		require.Equal(t, Warning, dropped[i].Severity)
		require.Equal(t, []string{fmt.Sprintf("capabilities[%d]", i+1)}, dropped[i].Source.Fields)
		require.Contains(t, dropped[i].Reason, `capability "`+capability+`"`)
		require.Contains(t, dropped[i].Reason, "cannot serve multiple genai categories")
	}

	out, err := result.Output.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(out), "genai_category: text/embeddings")
	require.NotContains(t, string(out), "image/v1/images/generations",
		"the skipped image capability must not leak a target into the output")
	require.NotContains(t, string(out), "video/v1/videos/generations",
		"the skipped video capability must not leak a target into the output")
}

// A Kong acl plugin permits exactly one of config.allow / config.deny
// (only_one_of in its schema). An AI Gateway acl that sets both is not
// representable as a single valid acl plugin, so the converter must reject it
// rather than emit structurally invalid config (it does no schema validation).
func TestConvertRejectsACLWithAllowAndDeny(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    access:
      acls:
        allow: [premium]
        deny: [banned]
    config:
      route: {paths: [/oidc-repro/model-a]}
      model: {alias: m1}
model_providers:
  - name: p1
    type: openai
`)
	// The config is invalid for every output layout, so it fails without -strict.
	_, _, err := convertDoc(src, false)
	require.Error(t, err, "acl with both allow and deny must be rejected")
	require.Contains(t, err.Error(), "allow")
	require.Contains(t, err.Error(), "deny")
}

// The acl plugin's only_one_of spans all four fields (config.allow,
// config.deny, config.allow_when, config.deny_when), so any pair is invalid,
// not just allow+deny. The per-field golden cases each populate one list, so
// the combinations are covered here.
func TestConvertRejectsACLWithMultipleFields(t *testing.T) {
	const tmpl = `
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    access:
      acls:
%s
    config:
      route: {paths: [/ai]}
model_providers:
  - name: p1
    type: openai
`
	cases := []struct {
		name  string
		acls  string
		wants []string
	}{
		{
			name: "allow_when and deny_when",
			acls: `        allow_when: ["principal.metadata.admin == 'true'"]
        deny_when: ["principal.metadata.banned == 'true'"]`,
			wants: []string{"allow_when", "deny_when"},
		},
		{
			name: "allow and allow_when",
			acls: `        allow: [premium]
        allow_when: ["principal.metadata.admin == 'true'"]`,
			wants: []string{"allow", "allow_when"},
		},
		{
			name: "deny and deny_when",
			acls: `        deny: [banned]
        deny_when: ["principal.metadata.banned == 'true'"]`,
			wants: []string{"deny", "deny_when"},
		},
		{
			name: "allow and deny_when",
			acls: `        allow: [premium]
        deny_when: ["principal.metadata.banned == 'true'"]`,
			wants: []string{"allow", "deny_when"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Convert([]byte(fmt.Sprintf(tmpl, tc.acls)), testOptions)
			require.NoError(t, err)
			fields := make([]string, len(tc.wants))
			for i, want := range tc.wants {
				fields[i] = "access.acls." + want
			}
			issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "guarded", fields...)
			for _, want := range tc.wants {
				require.Contains(t, issue.Reason, want)
			}
			require.Empty(t, result.Output.AIModels, "the model is left out")
		})
	}
}

// A single when-list stays valid: the guard must reject combinations only.
func TestConvertAcceptsACLWithOnlyAllowWhen(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    access:
      acls:
        allow_when: ["principal.metadata.admin == 'true'"]
    config:
      route: {paths: [/ai]}
model_providers:
  - name: p1
    type: openai
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues, "a lone allow_when must still convert")
	require.Len(t, result.Output.AIModels, 1)
}

// The skills API is served only as passthrough, so Kong rejects a llm/v1/skills
// target whose provider renders a different wire format: there is no
// cross-format conversion to fall back on. An openai-format model on an
// anthropic provider is such a pairing; an unresolvable provider type is left to
// the usual warning instead.
func TestConvertRejectsSkillsWithCrossFormatProvider(t *testing.T) {
	src := []byte(`
models:
  - type: api
    name: skills-api
    capabilities: [skills]
    formats: [{type: openai}]
    targets:
      - name: gpt-5.6
        provider: anthropic-prod
        config: {type: anthropic}
    config:
      route: {paths: [/ai]}
model_providers:
  - name: anthropic-prod
    type: anthropic
`)
	_, _, err := convertDoc(src, false)
	require.Error(t, err, "skills on a provider rendering another format must be rejected")
	require.Contains(t, err.Error(), "passthrough-only")
	require.Contains(t, err.Error(), "llm_format")
}

// A model's policies list must not reference an authentication policy
// (key-auth/openid-connect) directly: those require anonymous fallback and a
// companion anonymous consumer, which only the auth_strategies mechanism
// provides. Referencing one via policies is rejected rather than silently
// emitting an auth plugin with no anonymous fallback.
func TestConvertRejectsAuthPolicyOnModel(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [my-key-auth]
    config:
      route: {paths: [/oidc-repro/model-b]}
      model: {alias: m1}
model_providers:
  - name: p1
    type: openai
policies:
  - type: key-auth
    name: my-key-auth
    config: {key_names: [apikey]}
`)
	_, _, err := convertDoc(src, false)
	require.Error(t, err, "model policies referencing key-auth/openid-connect must be rejected")
	require.Contains(t, err.Error(), "auth_strategies")
}

func TestConvertRejectsAuthStrategyTypeOtherThanKeyAuthOrOIDC(t *testing.T) {
	tests := []struct {
		name   string
		entity string
		kind   string
		field  string
	}{
		{
			name: "model",
			entity: `
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    access:
      auth_strategies: [bad-type]
    config:
      route: {paths: [/ai]}
model_providers:
  - name: p1
    type: openai
`,
			kind:  kindModel,
			field: "access.auth_strategies[0]",
		},
		{
			name: "agent",
			entity: `
agents:
  - type: http
    name: guarded
    access:
      auth_strategies: [bad-type]
    config:
      url: https://agent.internal/api
      route: {paths: [/agents/guarded]}
`,
			kind:  kindAgent,
			field: "access.auth_strategies[0]",
		},
		{
			name: "mcp server",
			entity: `
mcp_servers:
  - type: listener
    name: guarded
    config:
      route: {paths: [/mcp/guarded]}
    access:
      auth_strategies: [bad-type]
    tools:
      - name: ping
        description: Ping
        method: GET
        path: /ping
        scheme: https
        host: guarded.internal
`,
			kind:  kindMCPServer,
			field: "access.auth_strategies[0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.entity + `
auth_strategies:
  - name: bad-type
    type: jwt
    config: {}
`
			result, err := Convert([]byte(src), testOptions)
			require.NoError(t, err)
			issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, tt.kind, "guarded", tt.field)
			require.Contains(t, issue.Reason, "must be key-auth or openid-connect")
			require.Empty(t, result.Output.Services, "the entity is left out")
		})
	}
}

// requestTerminationModel is a model with a policy that outranks the
// selector and a model-scoped request-termination policy. Its route has the
// auth strategies in strategies.
func requestTerminationModel(strategies string) []byte {
	return []byte(`
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [probe, block-model]
    access:
      auth_strategies: ` + strategies + `
    config:
      route:
        paths: [/ai]
        model:
          values: ["@kong/a", "@kong/b"]
model_providers:
  - name: p1
    type: openai
policies:
  - type: pre-function
    name: probe
    config: {access: ["return"]}
  - type: request-termination
    name: block-model
    config: {status_code: 403}
auth_strategies:
  - name: key-auth-default
    type: key-auth
    config: {key_names: [x-api-key]}
  - name: okta-oidc
    type: openid-connect
    config: {issuer: https://idp.example.com}
`)
}

// One auth plugin rejects the request itself. request-termination then stays
// out of the selector ordering, so the model-scoped policy runs.
func TestConvertSingleAuthStrategyKeepsModelScopedRequestTermination(t *testing.T) {
	result, err := Convert(requestTerminationModel("[key-auth-default]"), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Output.Consumers, "one auth strategy needs no anonymous consumer")

	var sawKeyAuth, sawSelector bool
	var terminations int
	for _, p := range result.Output.Plugins {
		switch p.Name {
		case "key-auth":
			sawKeyAuth = true
			require.NotContains(t, p.Config, "anonymous")
		case "ai-model-selector":
			sawSelector = true
			require.NotNil(t, p.Ordering)
			require.Equal(t, []string{"key-auth"}, p.Ordering.After.Access)
		case "request-termination":
			require.NotNil(t, p.Model, "the policy stays model-scoped")
			terminations++
		}
	}
	require.True(t, sawKeyAuth, "key-auth plugin not found")
	require.True(t, sawSelector, "ai-model-selector plugin not found")
	require.Equal(t, 2, terminations, "one copy per alias")
}

func TestConvertWarnsModelScopedRequestTerminationWithAnonymousFallback(t *testing.T) {
	result, err := Convert(requestTerminationModel("[key-auth-default, okta-oidc]"), testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, kindModel, "guarded", "policies[1]")
	require.Contains(t, issue.Reason, "model-scoped request-termination policy on this route never runs")
	var sawSelector bool
	for _, p := range result.Output.Plugins {
		if p.Name == "ai-model-selector" {
			sawSelector = true
			require.Contains(t, p.Ordering.After.Access, "request-termination")
		}
	}
	require.True(t, sawSelector, "ai-model-selector plugin not found")
}

func TestConvertScopesAuthStrategiesWithoutLeakingAcrossSharedRoutes(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: public-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-public
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/oidc-repro/model-a]}
      model: {alias: public}
  - type: model
    name: protected-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-protected
        provider: p1
        config: {type: openai}
    access:
      auth_strategies: [oidc]
    config:
      route: {paths: [/oidc-repro/model-b]}
      model: {alias: protected}
model_providers:
  - name: p1
    type: openai
auth_strategies:
  - name: oidc
    type: openid-connect
    config: {issuer: https://id.example.test}
`)

	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var doc struct {
		Plugins []struct {
			Name  string `yaml:"name"`
			Route string `yaml:"route"`
			Model string `yaml:"model"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var oidcRoute, publicRoute, protectedRoute string
	for _, plugin := range doc.Plugins {
		if plugin.Name == "openid-connect" {
			require.Empty(t, plugin.Model, "OIDC must be enforced at route scope")
			oidcRoute = plugin.Route
		}
		if plugin.Name == "ai-proxy-advanced" {
			switch plugin.Model {
			case "public-model":
				publicRoute = plugin.Route
			case "protected-model":
				protectedRoute = plugin.Route
			}
		}
	}
	require.NotEmpty(t, oidcRoute)
	require.Equal(t, protectedRoute, oidcRoute, "OIDC guards only the protected model's route")
	require.NotEqual(t, publicRoute, oidcRoute, "the public model must not share the OIDC route")
}

func TestConvertSharesAuthStrategyForUniformlyGuardedSharedRoute(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: model-a
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-a
        provider: p1
        config: {type: openai}
    access: {auth_strategies: [oidc]}
    config: {route: {paths: [/ai]}, model: {alias: a}}
  - type: model
    name: model-b
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-b
        provider: p1
        config: {type: openai}
    access: {auth_strategies: [oidc]}
    config: {route: {paths: [/ai]}, model: {alias: b}}
model_providers:
  - name: p1
    type: openai
auth_strategies:
  - name: oidc
    type: openid-connect
`)

	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var doc struct {
		Plugins []struct {
			Name  string `yaml:"name"`
			Route string `yaml:"route"`
			Model string `yaml:"model"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var count int
	for _, plugin := range doc.Plugins {
		if plugin.Name == "openid-connect" {
			count++
			require.Equal(t, "openai-chat", plugin.Route)
			require.Empty(t, plugin.Model, "uniformly guarded models share the route plugin")
		}
	}
	require.Equal(t, 1, count)
}

// The Kong acl plugin checks a legacy per-consumer kong.db.acls entity by
// default, not Enterprise consumer_groups membership, unless
// config.include_consumer_groups is set. AI Gateway's only group-membership
// construct is consumer_groups (the converter never creates legacy acls
// rows), so an emitted acl plugin must always set include_consumer_groups —
// otherwise its allow/deny list can never match anything and the plugin can
// never allow a request.
func TestConvertACLIncludesConsumerGroups(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    access:
      acls:
        allow: [premium-users]
    config:
      route: {paths: [/ai]}
      model: {alias: m1}
model_providers:
  - name: p1
    type: openai
`)
	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var doc struct {
		Plugins []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var acl *struct {
		Name   string
		Config map[string]any
	}
	for i := range doc.Plugins {
		if doc.Plugins[i].Name == "acl" {
			acl = &struct {
				Name   string
				Config map[string]any
			}{doc.Plugins[i].Name, doc.Plugins[i].Config}
		}
	}
	require.NotNil(t, acl, "expected an acl plugin in the output")
	require.Equal(t, true, acl.Config["include_consumer_groups"],
		"acl plugin must set include_consumer_groups so consumer_groups-based allow/deny actually works")
}

// ai-mcp-proxy has the identical include_consumer_groups gap as the Kong acl
// plugin (see TestConvertACLIncludesConsumerGroups above): its own ACL-subject
// extraction only recognizes consumer_groups membership when the flag is set,
// defaulting to false. mcpPlugin() must set it by default — including for an
// MCP server with no ACL configured at all (open-mcp below), which proves the
// fix doesn't only trigger when default_acl/tools[].acl is present — except
// when acl_attribute_type is oauth_access_token (oauth-mcp below), where
// ai-mcp-proxy's own schema hard-rejects include_consumer_groups being set.
func TestConvertMCPIncludesConsumerGroups(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: guarded-mcp
    access:
      default_tool_acls:
        allow: [premium-users]
    config:
      route: {paths: [/mcp/guarded]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
  - type: conversion-listener
    name: open-mcp
    config:
      route: {paths: [/mcp/open]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
  - type: conversion-listener
    name: oauth-mcp
    access:
      acl_attribute_type: oauth_access_token
      access_token_claim_field: .user.email
      default_tool_acls:
        allow: [premium-users]
    config:
      route: {paths: [/mcp/oauth]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
`)
	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var doc struct {
		Plugins []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc), "parse output")

	var mcpPlugins []map[string]any
	for i := range doc.Plugins {
		if doc.Plugins[i].Name == "ai-mcp-proxy" {
			mcpPlugins = append(mcpPlugins, doc.Plugins[i].Config)
		}
	}
	require.Len(t, mcpPlugins, 3, "expected an ai-mcp-proxy plugin per MCP server")
	for _, cfg := range mcpPlugins[:2] {
		require.Equal(t, true, cfg["include_consumer_groups"],
			"ai-mcp-proxy plugin must set include_consumer_groups so consumer_groups-based allow/deny actually works")
	}
	// oauth_access_token is the exception: ai-mcp-proxy's schema hard-rejects
	// include_consumer_groups being set in that mode (and subjects.lua ignores it anyway),
	// so it must be left unset, not forced to true.
	_, isSet := mcpPlugins[2]["include_consumer_groups"]
	require.False(t, isSet,
		"ai-mcp-proxy plugin must NOT set include_consumer_groups when "+
			"acl_attribute_type is oauth_access_token: Kong's schema rejects that")
}

func TestConvertMetadataLocationsMatchLayout(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [chat]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {alias: m1}
model_providers:
  - name: p1
    type: openai
`)

	doc, _, err := convertDoc(src, false)
	require.NoError(t, err)

	require.Equal(t, "services[0].routes[0]", doc.Metadata().Routes[0].Location, "decK locations are nested")
	require.Equal(t, "routes[0]", doc.ToDBLess().Metadata().Routes[0].Location, "db-less locations are flat")
}

func TestConvertDBLessPreservesProvidedPolicyIDAndGeneratesMissingOnes(t *testing.T) {
	src := []byte(`
policies:
  - id: provided-policy-id
    type: rate-limiting
    name: org-wide-limit
    global: true
    config:
      minute: 1000
      policy: local
  - type: key-auth
    name: require-key
    global: true
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")
	require.Len(t, plugins, 2)

	pluginsByName := make(map[string]map[string]any, len(plugins))
	for _, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		require.True(t, ok, "expected plugin entry")
		pluginsByName[plugin["name"].(string)] = plugin
	}

	require.Equal(t, "provided-policy-id", pluginsByName["rate-limiting"]["id"])

	keyAuthID, ok := pluginsByName["key-auth"]["id"].(string)
	require.True(t, ok, "expected generated key-auth plugin id")
	require.NotEmpty(t, keyAuthID)
	require.NotEqual(t, "provided-policy-id", keyAuthID)
}

func TestConvertRateLimitingProviderEntityMatchUsesProviderType(t *testing.T) {
	src := []byte(`
policies:
  - name: provider-limit
    type: ai-rate-limiting-advanced
    global: true
    config:
      identifier: ip
      strategy: local
      policies:
        - match:
            - type: provider
              values: [poc-openai-provider, "open*", unknown-provider]
          limits:
            - limit: 10
              window_size: 20
              tokens_count_strategy: total_tokens
model_providers:
  - name: poc-openai-provider
    type: openai
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got struct {
		Plugins []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	var config map[string]any
	for _, plugin := range got.Plugins {
		if plugin.Name == "ai-rate-limiting-advanced" {
			config = plugin.Config
			break
		}
	}
	require.NotNil(t, config, "expected ai-rate-limiting-advanced plugin")

	policies := config["policies"].([]any)
	matches := policies[0].(map[string]any)["match"].([]any)
	values := matches[0].(map[string]any)["values"]
	require.Equal(t, []any{"openai", "open*", "unknown-provider"}, values)
}

func TestConvertMapsConfiguredModelName(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1], model: {values: ["@openai/custom-m1"]}}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai_models entry")
	require.Equal(t, "@openai/custom-m1", aiModel["name"])
	_, hasAIModelAlias := aiModel["alias"]
	require.False(t, hasAIModelAlias, "ai-model alias should be unset")

	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")

	var proxy map[string]any
	for _, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		require.True(t, ok, "expected plugin entry")
		if plugin["name"] == "ai-proxy-advanced" {
			proxy = plugin
			break
		}
	}
	require.NotNil(t, proxy, "expected ai-proxy-advanced plugin")

	cfg, ok := proxy["config"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced config")
	targets, ok := cfg["targets"].([]any)
	require.True(t, ok, "expected ai-proxy-advanced targets")
	require.Len(t, targets, 1)
	target, ok := targets[0].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced target")
	model, ok := target["model"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced model")
	require.Equal(t, "gpt-4o", model["name"])
	require.Equal(t, "@openai/custom-m1", model["model_alias"], "target model_alias should match source model.alias")
}

func TestConvertDefaultSelectorUsesFormatDefault(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: gemini-model
    capabilities: [generate]
    formats: [{type: gemini}]
    targets:
      - name: gemini-2.5-pro
        provider: p1
        config: {type: gemini}
    config:
      route: {paths: [/v1], model: {values: ["@kong/gemini"]}}
model_providers:
  - name: p1
    type: gemini
`)

	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	plugins := got["plugins"].([]any)
	for _, raw := range plugins {
		plugin := raw.(map[string]any)
		if plugin["name"] != "ai-model-selector" {
			continue
		}
		config := plugin["config"].(map[string]any)
		sources, ok := config["sources"].([]any)
		require.True(t, ok, "default selector should use config.sources")
		require.Len(t, sources, 1)
		source := sources[0].(map[string]any)
		require.Equal(t, "path", source["source"])
		require.Equal(t, "models/([%w%.%-]+):", source["path_pattern"])
		return
	}
	t.Fatal("expected ai-model-selector plugin")
}

func TestConvertSynthesizesAIModelNameWhenUnset(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai_models entry")
	require.Equal(t, "m1", aiModel["name"])
	_, hasAIModelAlias := aiModel["alias"]
	require.False(t, hasAIModelAlias, "ai-model alias should be unset")

	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")

	var proxy map[string]any
	for _, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		require.True(t, ok, "expected plugin entry")
		if plugin["name"] == "ai-proxy-advanced" {
			proxy = plugin
			break
		}
	}
	require.NotNil(t, proxy, "expected ai-proxy-advanced plugin")

	cfg, ok := proxy["config"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced config")
	targets, ok := cfg["targets"].([]any)
	require.True(t, ok, "expected ai-proxy-advanced targets")
	require.Len(t, targets, 1)
	target, ok := targets[0].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced target")
	model, ok := target["model"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced model")
	require.Equal(t, "gpt-4o", model["name"])
	modelAlias, ok := model["model_alias"]
	require.True(t, ok, "expected ai-proxy-advanced model_alias")
	require.Equal(t, "m1", modelAlias)
}

func TestConvertDBLessSynthesizesAIModelNameWhenUnset(t *testing.T) {
	src := []byte(`
models:
  - type: api
    name: files-api
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {name_header: false}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal db-less output")

	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, 1)
	aiModel, ok := aiModels[0].(map[string]any)
	require.True(t, ok, "expected ai_models entry")
	require.Equal(t, "files-api", aiModel["name"])
	_, hasAIModelAlias := aiModel["alias"]
	require.False(t, hasAIModelAlias, "ai-model alias should be unset")

	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")

	var proxy map[string]any
	for _, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		require.True(t, ok, "expected plugin entry")
		if plugin["name"] == "ai-proxy-advanced" {
			proxy = plugin
			break
		}
	}
	require.NotNil(t, proxy, "expected ai-proxy-advanced plugin")

	cfg, ok := proxy["config"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced config")
	targets, ok := cfg["targets"].([]any)
	require.True(t, ok, "expected ai-proxy-advanced targets")
	require.Len(t, targets, 1)
	target, ok := targets[0].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced target")
	model, ok := target["model"].(map[string]any)
	require.True(t, ok, "expected ai-proxy-advanced model")
	require.Equal(t, "files", model["name"])
	// type:api (files/batches) requests carry no body model, so the target must
	// stay alias-less to remain in the balancer's "<default>" pool the request
	// falls back to; an alias would strand it (500 "failed to get balancer instance").
	_, hasModelAlias := model["model_alias"]
	require.False(t, hasModelAlias, "type:api target must be alias-less")
}

func TestConvertLeavesOutTargetWithUnknownProvider(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: missing-provider
        config: {type: openai}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route: {paths: [/chat]}
      model: {}
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 2)
	requireIssue(t, result.Issues, Warning, UndefinedReference, kindModel, "m1", "targets[0].provider")
	requireIssue(t, result.Issues, Error, MissingRequiredConfigField, kindModel, "m1", "targets")
	require.Empty(t, result.Output.AIModels, "a model without targets is left out")
	require.Empty(t, result.Output.Services)
}

func TestConvertUnknownPolicyKeepsEntity(t *testing.T) {
	src := []byte(`
consumers:
  - name: c1
    type: api-key
    policies: [missing-policy]
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	requireIssue(t, result.Issues, Error, UndefinedReference, kindConsumer, "c1", "policies[0]")
	require.Len(t, result.Output.Consumers, 1, "the consumer converts without the policy")
	require.Empty(t, result.Output.Consumers[0].Plugins)
}

func TestConvertUnknownDatastoreOnReferencedPolicy(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: rag-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [rag-injector]
    config:
      route: {paths: [/rag]}
model_providers:
  - name: p1
    type: openai
policies:
  - type: ai-rag-injector
    name: rag-injector
    config:
      vectordb: {strategy: redis, dimensions: 1536, distance_metric: cosine}
    datastores: [{name: missing-datastore}]
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	requireIssue(t, result.Issues, Error, UndefinedReference, kindPolicy, "rag-injector", "datastores[0].name")
	require.Len(t, result.Output.AIModels, 1, "the model converts")
}

// A dangling datastore reference keeps the policy without a connection.
func TestConvertUnknownDatastoreOnGlobalPolicy(t *testing.T) {
	src := []byte(`
policies:
  - type: rate-limiting
    name: limiter
    global: true
    config: {minute: 100, policy: redis}
    datastores: [{name: missing-datastore}]
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Error, UndefinedReference, kindPolicy, "limiter", "datastores[0].name")
	require.Contains(t, issue.Reason, `references unknown datastore "missing-datastore"`)
	require.Len(t, result.Output.Plugins, 1, "the policy converts without the connection")
}

func TestConvertRejectsRedisCEDatastoreOnModel(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    datastores: [{name: ds}]
    config:
      route: {paths: [/ai]}
      balancer:
        algorithm: semantic
        vectordb: {strategy: redis, dimensions: 1536, distance_metric: cosine}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: redis-ce
    name: ds
    config: {host: redis-ce.internal}
`)
	_, _, err := convertDoc(src, false)
	require.Error(t, err, "models never accept a redis-ce datastore")
	require.Contains(t, err.Error(), "m1")
	require.Contains(t, err.Error(), "redis-ce")
}

func TestConvertRejectsMismatchedVectorDBStrategy(t *testing.T) {
	for _, tc := range []struct {
		name          string
		strategy      string
		datastoreType string
	}{
		{name: "redis strategy with vectordb datastore", strategy: "redis", datastoreType: "vectordb"},
		{name: "pgvector strategy with redis-ee datastore", strategy: "pgvector", datastoreType: "redis-ee"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    datastores: [{name: ds}]
    config:
      route: {paths: [/ai]}
      balancer:
        algorithm: semantic
        vectordb: {strategy: ` + tc.strategy + `, dimensions: 1536, distance_metric: cosine}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: ` + tc.datastoreType + `
    name: ds
    config: {host: store.internal}
`)
			_, _, err := convertDoc(src, false)
			require.Error(t, err, "vectordb strategy must match the datastore type")
			require.Contains(t, err.Error(), "m1")
			require.Contains(t, err.Error(), tc.strategy)
			require.Contains(t, err.Error(), tc.datastoreType)
		})
	}
}

func TestConvertRejectsMultipleDatastoresOnModel(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    datastores: [{name: ds1}, {name: ds2}]
    config:
      route: {paths: [/ai]}
      balancer: {algorithm: semantic}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: vectordb
    name: ds1
    config: {host: pg.internal}
  - type: vectordb
    name: ds2
    config: {host: pg2.internal}
`)
	_, _, err := convertDoc(src, false)
	require.Error(t, err, "convert")
	require.Contains(t, err.Error(), "only one is allowed")
}

func TestConvertRejectsDatastoreOnNonSemanticModel(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    datastores: [{name: ds}]
    config:
      route: {paths: [/ai]}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: vectordb
    name: ds
    config: {host: pg.internal}
`)
	_, _, err := convertDoc(src, false)
	require.Error(t, err, "a datastore needs the semantic balancer's vector store")
	require.Contains(t, err.Error(), "semantic")
}

// A dangling datastore reference keeps the model without a connection.
func TestConvertUnknownDatastoreOnModel(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    datastores: [{name: missing-datastore}]
    config:
      route: {paths: [/ai]}
      balancer:
        algorithm: semantic
        vectordb: {strategy: pgvector, dimensions: 1024, distance_metric: cosine}
model_providers:
  - name: p1
    type: openai
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	requireIssue(t, result.Issues, Error, UndefinedReference, kindModel, "m1", "datastores[0].name")
	require.Len(t, result.Output.AIModels, 1, "the model converts")
}

func TestConvertRejectsMixedAPIDatastoresOnSharedRoute(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  []byte
	}{
		{
			name: "datastore then none",
			src: []byte(`
models:
  - type: api
    name: files-api
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files
        provider: p1
        config: {type: openai}
    datastores: [{name: ds}]
    config:
      route: {paths: [/v1]}
      balancer: {algorithm: semantic}
  - type: api
    name: files-api-2
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files-2
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: vectordb
    name: ds
    config: {host: pg.internal}
`),
		},
		{
			name: "none then datastore",
			src: []byte(`
models:
  - type: api
    name: files-api
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
  - type: api
    name: files-api-2
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files-2
        provider: p1
        config: {type: openai}
    datastores: [{name: ds}]
    config:
      route: {paths: [/v1]}
      balancer: {algorithm: semantic}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: vectordb
    name: ds
    config: {host: pg.internal}
`),
		},
		{
			name: "different datastores",
			src: []byte(`
models:
  - type: api
    name: files-api
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files
        provider: p1
        config: {type: openai}
    datastores: [{name: ds1}]
    config:
      route: {paths: [/v1]}
      balancer: {algorithm: semantic}
  - type: api
    name: files-api-2
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: files-2
        provider: p1
        config: {type: openai}
    datastores: [{name: ds2}]
    config:
      route: {paths: [/v1]}
      balancer: {algorithm: semantic}
model_providers:
  - name: p1
    type: openai
datastores:
  - type: vectordb
    name: ds1
    config: {host: pg-1.internal}
  - type: redis-ee
    name: ds2
    config: {host: redis.internal}
`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := convertDoc(tc.src, false)
			require.Error(t, err, "convert must reject datastore mixing on a shared route-only api plugin")
			require.Contains(t, err.Error(), "cannot share route")
		})
	}
}

func TestConvertModelDatastoreReplacesInlineConnection(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    datastores: [{name: ds}]
    config:
      route: {paths: [/ai]}
      balancer:
        algorithm: semantic
        vectordb:
          strategy: pgvector
          dimensions: 1024
          distance_metric: cosine
          threshold: 0.7
          host: placeholder.invalid
model_providers:
  - name: p1
    type: openai
datastores:
  - type: vectordb
    name: ds
    config: {host: pg.internal, port: 5432}
`)
	out, _, err := convertDoc(src, false)
	require.NoError(t, err, "convert")
	var plugin map[string]any
	for _, p := range out.Plugins {
		if p.Name == "ai-proxy-advanced" {
			plugin = p.Config
			break
		}
	}
	require.NotNil(t, plugin, "ai-proxy-advanced plugin emitted")
	vectordb := plugin["vectordb"].(map[string]any)
	require.Equal(t, 1024, vectordb["dimensions"], "common keys stay on the model")
	require.Equal(t, map[string]any{"host": "pg.internal", "port": 5432},
		vectordb["pgvector"], "datastore connection replaces the inline one")
}

// A tool without a description is left out. The other tools of the server
// still convert.
func TestConvertLeavesOutMCPToolMissingDescription(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: m1
    config:
      route: {paths: [/mcp]}
    tools:
      - name: describedTool
        description: A tool
      - name: noDescTool
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err, "convert")
	require.Len(t, result.Issues, 1)
	issue := requireIssue(t, result.Issues, Warning, MissingRequiredConfigField, kindMCPServer, "m1",
		"tools[1].description")
	require.Contains(t, issue.Reason, "no description")

	require.Len(t, result.Output.Services, 1, "the server still converts")
	plugin := result.Output.Services[0].Routes[0].Plugins[0]
	require.Equal(t, "ai-mcp-proxy", plugin.Name)
	tools := plugin.Config["tools"].([]map[string]any)
	require.Len(t, tools, 1)
	require.Equal(t, "describedTool", tools[0]["name"])
}

func TestConvertConversionOnlyMCPServerStripPath(t *testing.T) {
	// A conversion-only server's route is only ever re-entered by the DP to
	// resolve a Tool's config.url-relative path; that only works if strip_path
	// removes the route's own path prefix first. An explicit strip_path: false
	// is forced to true (with a warning, checked separately); true and unset
	// pass through unmodified.
	tests := []struct {
		name      string
		routeYAML string
		wantNil   bool
		want      bool
	}{
		{name: "explicit false forced to true", routeYAML: `route: {paths: [/toolset], strip_path: false}`, want: true},
		{name: "explicit true", routeYAML: `route: {paths: [/toolset], strip_path: true}`, want: true},
		{name: "unset", routeYAML: `route: {paths: [/toolset]}`, wantNil: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(`
mcp_servers:
  - type: conversion-only
    name: toolset
    config:
      ` + tc.routeYAML + `
  - type: listener
    name: gateway
    config:
      route: {paths: [/gateway]}
      server: {tag: bucket1}
      sources: [toolset]
`)
			out, _, err := encodeDBLessYAML(src, testOptions)
			require.NoError(t, err, "convert db-less")

			var got struct {
				Routes []struct {
					Name      string `yaml:"name"`
					StripPath *bool  `yaml:"strip_path"`
				} `yaml:"routes"`
			}
			require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

			var found bool
			for _, r := range got.Routes {
				if r.Name == "toolset-route" {
					found = true
					if tc.wantNil {
						require.Nil(t, r.StripPath, "expected strip_path to be left unset")
						return
					}
					require.NotNil(t, r.StripPath, "expected strip_path to be set")
					require.Equal(t, tc.want, *r.StripPath, "unexpected strip_path value")
				}
			}
			require.True(t, found, "expected toolset-route in output")
		})
	}
}

func TestConvertWarnsConversionOnlyMCPServerStripPathFalse(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-only
    name: toolset
    config:
      route: {paths: [/toolset], strip_path: false}
  - type: listener
    name: gateway
    config:
      route: {paths: [/gateway]}
      server: {tag: bucket1}
      sources: [toolset]
`)
	_, warnings, err := convertDoc(src, false)
	require.NoError(t, err, "convert")
	require.Contains(t, strings.Join(warnings, "\n"), "strip_path will be overridden to true",
		"expected a warning when strip_path is false")
}

func TestConvertDoesNotWarnConversionOnlyMCPServerStripPathUnsetOrTrue(t *testing.T) {
	for _, tc := range []string{
		`route: {paths: [/toolset]}`,
		`route: {paths: [/toolset], strip_path: true}`,
	} {
		src := []byte(`
mcp_servers:
  - type: conversion-only
    name: toolset
    config:
      ` + tc + `
  - type: listener
    name: gateway
    config:
      route: {paths: [/gateway]}
      server: {tag: bucket1}
      sources: [toolset]
`)
		_, warnings, err := convertDoc(src, false)
		require.NoError(t, err, "convert")
		require.NotContains(t, strings.Join(warnings, "\n"), "strip_path",
			"did not expect a strip_path warning when it is unset or already true")
	}
}

func TestConvertReportsConversionOnlyMCPServerStripPathFalse(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-only
    name: toolset
    config:
      route: {paths: [/toolset], strip_path: false}
  - type: listener
    name: gateway
    config:
      route: {paths: [/gateway]}
      server: {tag: bucket1}
      sources: [toolset]
`)
	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	requireIssue(t, result.Issues, Warning, ConfigFieldOverridden, kindMCPServer, "toolset",
		"config.route.strip_path")
}

func TestConvertDoesNotForceStripPathOnOtherMCPServerTypes(t *testing.T) {
	// Only conversion-only servers re-enter Kong at their own route to resolve
	// a config.url-relative Tool path, so strip_path is left alone for every
	// other mode.
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: m1
    config:
      route: {paths: [/mcp], strip_path: false}
`)
	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got struct {
		Routes []struct {
			Name      string `yaml:"name"`
			StripPath *bool  `yaml:"strip_path"`
		} `yaml:"routes"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Routes, 1, "expected 1 route")
	require.NotNil(t, got.Routes[0].StripPath, "expected strip_path to be set")
	require.False(t, *got.Routes[0].StripPath, "expected strip_path to remain false for conversion-listener")
}

func TestA2APluginDropsLogAudits(t *testing.T) {
	a := &aigw.Agent{
		Type: "a2a",
		Name: "agent1",
		Config: aigw.AgentConfig{
			Logging: &aigw.Logging{
				Statistics: boolPtr(true),
				Audits:     boolPtr(true), // ai-mcp-proxy field; invalid for ai-a2a-proxy
			},
		},
	}
	plugin, err := (&Converter{}).a2aPlugin(issueSource(kindAgent, a.Name), a)
	require.NoError(t, err)
	logging, ok := plugin.Config["logging"].(map[string]any)
	require.True(t, ok, "expected logging block, got %v", plugin.Config["logging"])
	require.NotContains(t, logging, "log_audits", "ai-a2a-proxy must not emit log_audits, got %v", logging)
	require.Equal(t, true, logging["log_statistics"], "expected log_statistics true")
}

func TestConvertDisabledAgentDisablesService(t *testing.T) {
	src := []byte(`
agents:
  - type: a2a
    name: off-agent
    enabled: false
    config:
      url: https://a.internal/a2a
      route: {paths: [/agents/off]}
  - type: http
    name: on-agent
    enabled: true
    config:
      url: https://b.internal/api
      route: {paths: [/agents/on]}
`)
	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got struct {
		Services []struct {
			Name    string `yaml:"name"`
			Enabled *bool  `yaml:"enabled"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	byName := map[string]*bool{}
	for _, s := range got.Services {
		byName[s.Name] = s.Enabled
	}
	require.Contains(t, byName, "off-agent")
	require.NotNil(t, byName["off-agent"], "disabled agent must emit enabled")
	require.False(t, *byName["off-agent"], "disabled agent service must be enabled=false")
	require.Nil(t, byName["on-agent"], "enabled agent should not emit the flag")
}

func TestConvertAgentAuthStrategiesBecomeRoutePlugins(t *testing.T) {
	src := []byte(`
auth_strategies:
  - name: agent-key-auth
    type: key-auth
    config:
      key_names: [apikey]
agents:
  - type: http
    name: protected-agent
    access:
      acls: {allow: [allowed-group]}
      auth_strategies: [agent-key-auth]
    config:
      url: https://example.test
      route: {paths: [/protected-agent]}
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err)

	var got struct {
		Plugins []struct {
			Name   string         `yaml:"name"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got))
	pluginsByName := make(map[string]map[string]any, len(got.Plugins))
	for _, plugin := range got.Plugins {
		pluginsByName[plugin.Name] = plugin.Config
	}
	require.Contains(t, pluginsByName, "acl")
	require.Contains(t, pluginsByName, "key-auth")
	require.Equal(t, "anonymous", pluginsByName["key-auth"]["anonymous"])
	require.Contains(t, pluginsByName, "request-termination")
}

func TestConvertDisabledMCPServerDisablesService(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-listener
    name: off-mcp
    enabled: false
    config:
      route: {paths: [/mcp/off]}
    tools:
      - {name: t, description: a tool, method: GET, path: /t, scheme: https, host: x.internal}
  - type: passthrough-listener
    name: on-mcp
    enabled: true
    upstream_url: https://b.internal/mcp
    config:
      route: {paths: [/mcp/on]}
`)
	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got struct {
		Services []struct {
			Name    string `yaml:"name"`
			Enabled *bool  `yaml:"enabled"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	byName := map[string]*bool{}
	for _, s := range got.Services {
		byName[s.Name] = s.Enabled
	}
	require.Contains(t, byName, "off-mcp")
	require.NotNil(t, byName["off-mcp"], "disabled MCP server must emit enabled")
	require.False(t, *byName["off-mcp"], "disabled MCP server service must be enabled=false")
	require.Nil(t, byName["on-mcp"], "enabled MCP server should not emit the flag")
}

func TestConvertLeavesOutDisabledModel(t *testing.T) {
	src := []byte(`
model_providers:
  - name: openai
    type: openai
models:
  - type: model
    name: disabled
    enabled: false
    capabilities: [agentic, generate, image, video]
    formats: [{type: openai}]
    targets: [{name: gpt-5, provider: openai, config: {type: openai}}]
    config:
      route: {paths: [/gpt-chat]}
  - type: api
    name: disabled-api
    enabled: false
    capabilities: [files]
    targets: [{name: gpt-5, provider: openai, config: {type: openai}}]
  - type: model
    name: disabled-passthrough
    enabled: false
    formats: [{type: passthrough}]
    targets: [{name: gpt-5, provider: openai, config: {type: openai}}]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Output.Services)
	require.Empty(t, result.Output.Routes)
	require.Empty(t, result.Output.Plugins)
	require.Empty(t, result.Output.AIModels)
}

func TestConvertModelsWithDifferentRoutesDoNotShareEndpointRoute(t *testing.T) {
	src := []byte(`
model_providers:
  - name: xai
    type: xai
  - name: openai
    type: openai
models:
  - type: model
    name: xai-chat
    formats: [{type: openai}]
    capabilities: [generate]
    targets: [{name: grok-4, provider: xai, config: {type: xai}}]
    config:
      route: {paths: [/xai-chat]}
      model: {alias: xai-chat}
  - type: model
    name: openai-chat
    formats: [{type: openai}]
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai, config: {type: openai}}]
    config:
      route: {paths: [/openai-chat]}
      model: {alias: openai-chat}
`)

	out, _, err := convertYAML(src)
	require.NoError(t, err, "convert")

	var got struct {
		Services []struct {
			Routes []struct {
				Name  string   `yaml:"name"`
				Paths []string `yaml:"paths"`
			} `yaml:"routes"`
		} `yaml:"services"`
		Plugins []struct {
			Name  string `yaml:"name"`
			Route string `yaml:"route"`
			Model string `yaml:"model"`
		} `yaml:"plugins"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Services, 1)
	require.Len(t, got.Services[0].Routes, 2)

	routeByPath := map[string]string{}
	for _, route := range got.Services[0].Routes {
		require.Len(t, route.Paths, 1)
		routeByPath[route.Paths[0]] = route.Name
	}
	require.Equal(t, "openai-chat", routeByPath["/xai-chat/chat/completions"])
	require.Equal(t, "openai-chat-2", routeByPath["/openai-chat/chat/completions"])

	pluginRouteByModel := map[string]string{}
	for _, plugin := range got.Plugins {
		if plugin.Name == "ai-proxy-advanced" {
			pluginRouteByModel[plugin.Model] = plugin.Route
		}
	}
	require.Equal(t, routeByPath["/xai-chat/chat/completions"], pluginRouteByModel["xai-chat"])
	require.Equal(t, routeByPath["/openai-chat/chat/completions"], pluginRouteByModel["openai-chat"])
}

func TestConvertDBLessFlattensConsumerCredentialsAndGroups(t *testing.T) {
	src := []byte(`
consumer_groups:
  - name: devs
consumers:
  - name: alice
    type: api-key
    consumer_groups: [devs]
    credentials:
      - name: alice-key
        api_key: sk-test
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	require.Equal(t, "3.0", got["_format_version"], "unexpected format version")
	require.Contains(t, got, "keyauth_credentials", "expected keyauth_credentials in db-less output: %s", out)
	require.Contains(t, got, "consumer_group_consumers", "expected consumer_group_consumers in db-less output: %s", out)
}

func TestConvertDBLessPreservesInputIDsWhenProvided(t *testing.T) {
	src := []byte(`
consumer_groups:
  - id: cg-source-id
    name: devs
consumers:
  - id: consumer-source-id
    name: alice
    type: api-key
    consumer_groups: [devs]
    credentials:
      - id: cred-source-id
        name: alice-key
        api_key: sk-test
models:
  - id: model-source-id
    type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/v1]}
      model: {alias: m1}
vaults:
  - id: vault-source-id
    type: env
    name: env
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	consumerGroups := got["consumer_groups"].([]any)
	consumerGroup := consumerGroups[0].(map[string]any)
	if consumerGroup["id"] != "cg-source-id" {
		t.Fatalf("expected consumer group id to be preserved, got %v", consumerGroup["id"])
	}

	consumers := got["consumers"].([]any)
	consumer := consumers[0].(map[string]any)
	if consumer["id"] != "consumer-source-id" {
		t.Fatalf("expected consumer id to be preserved, got %v", consumer["id"])
	}

	credentials := got["keyauth_credentials"].([]any)
	credential := credentials[0].(map[string]any)
	if credential["id"] != "cred-source-id" {
		t.Fatalf("expected credential id to be preserved, got %v", credential["id"])
	}

	members := got["consumer_group_consumers"].([]any)
	member := members[0].(map[string]any)
	if member["consumer_group"] != "cg-source-id" {
		t.Fatalf("expected consumer group membership to use preserved group id, got %v", member["consumer_group"])
	}
	if member["consumer"] != "consumer-source-id" {
		t.Fatalf("expected consumer group membership to use preserved consumer id, got %v", member["consumer"])
	}

	models := got["ai_models"].([]any)
	model := models[0].(map[string]any)
	if model["id"] != "model-source-id" {
		t.Fatalf("expected ai model id to be preserved, got %v", model["id"])
	}

	vaults := got["vaults"].([]any)
	vault := vaults[0].(map[string]any)
	if vault["id"] != "vault-source-id" {
		t.Fatalf("expected vault id to be preserved, got %v", vault["id"])
	}
}

func TestConvertDBLessKeepsExpandedRouteFields(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: m1
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: []
    access:
      acls: {allow: [], deny: []}
    config:
      route:
        hosts: [ai.example.com]
        methods: [GET, POST]
        protocols: [http, https]
        headers:
          x-api-version: [v1]
        snis: [ai.example.com]
        sources:
          - ip: 192.168.1.0/24
            port: 8080
        destinations:
          - ip: 10.1.0.0/16
            port: 443
        strip_path: true
        preserve_host: false
        https_redirect_status_code: 426
        regex_priority: 1
        path_handling: v0
        request_buffering: true
        response_buffering: true
      model: {}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	require.NoError(t, err, "convert db-less")

	var got struct {
		Routes []struct {
			Hosts     []string            `yaml:"hosts"`
			Methods   []string            `yaml:"methods"`
			Protocols []string            `yaml:"protocols"`
			Headers   map[string][]string `yaml:"headers"`
			SNIs      []string            `yaml:"snis"`
			Sources   []struct {
				IP   string `yaml:"ip"`
				Port int    `yaml:"port"`
			} `yaml:"sources"`
			Destinations []struct {
				IP   string `yaml:"ip"`
				Port int    `yaml:"port"`
			} `yaml:"destinations"`
			StripPath               *bool  `yaml:"strip_path"`
			PreserveHost            *bool  `yaml:"preserve_host"`
			HTTPSRedirectStatusCode *int   `yaml:"https_redirect_status_code"`
			RegexPriority           *int   `yaml:"regex_priority"`
			PathHandling            string `yaml:"path_handling"`
			RequestBuffering        *bool  `yaml:"request_buffering"`
			ResponseBuffering       *bool  `yaml:"response_buffering"`
		} `yaml:"routes"`
	}
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")
	require.Len(t, got.Routes, 1, "expected 1 route: %s", out)
	route := got.Routes[0]
	require.Equal(t, []string{"ai.example.com"}, route.Hosts, "unexpected hosts")
	require.Equal(t, []string{"GET", "POST"}, route.Methods, "unexpected methods")
	require.Equal(t, []string{"http", "https"}, route.Protocols, "unexpected protocols")
	require.Equal(t, []string{"v1"}, route.Headers["x-api-version"], "unexpected headers")
	require.Equal(t, []string{"ai.example.com"}, route.SNIs, "unexpected snis")
	require.Len(t, route.Sources, 1, "unexpected sources")
	require.Equal(t, "192.168.1.0/24", route.Sources[0].IP, "unexpected source ip")
	require.Equal(t, 8080, route.Sources[0].Port, "unexpected source port")
	require.Len(t, route.Destinations, 1, "unexpected destinations")
	require.Equal(t, "10.1.0.0/16", route.Destinations[0].IP, "unexpected destination ip")
	require.Equal(t, 443, route.Destinations[0].Port, "unexpected destination port")
	require.NotNil(t, route.StripPath, "unexpected strip_path")
	require.True(t, *route.StripPath, "unexpected strip_path")
	require.NotNil(t, route.PreserveHost, "unexpected preserve_host")
	require.False(t, *route.PreserveHost, "unexpected preserve_host")
	require.NotNil(t, route.HTTPSRedirectStatusCode, "unexpected https_redirect_status_code")
	require.Equal(t, 426, *route.HTTPSRedirectStatusCode, "unexpected https_redirect_status_code")
	require.NotNil(t, route.RegexPriority, "unexpected regex_priority")
	require.Equal(t, 1, *route.RegexPriority, "unexpected regex_priority")
	require.Equal(t, "v0", route.PathHandling, "unexpected path_handling")
	require.NotNil(t, route.RequestBuffering, "unexpected request_buffering")
	require.True(t, *route.RequestBuffering, "unexpected request_buffering")
	require.NotNil(t, route.ResponseBuffering, "unexpected response_buffering")
	require.True(t, *route.ResponseBuffering, "unexpected response_buffering")
}

func TestConvertDBLessCredentialTTLAndTags(t *testing.T) {
	ttl := 7200
	src := []byte(`
consumers:
  - name: alice
    type: api-key
    credentials:
      - name: alice-key
        type: api-key
        api_key: sk-alice
        ttl: 7200
        labels:
          env: prod
          scope: read-only
  - name: bob
    type: api-key
    credentials:
      - name: bob-key
        type: api-key
        api_key: sk-bob
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Credentials []struct {
			Key      string   `yaml:"key"`
			Consumer string   `yaml:"consumer"`
			TTL      *int     `yaml:"ttl"`
			Tags     []string `yaml:"tags"`
		} `yaml:"keyauth_credentials"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(got.Credentials) != 2 {
		t.Fatalf("expected 2 keyauth_credentials, got %d: %s", len(got.Credentials), out)
	}

	alice := got.Credentials[0]
	if alice.Key != "sk-alice" {
		t.Fatalf("unexpected key: %q", alice.Key)
	}
	if alice.TTL == nil || *alice.TTL != ttl {
		t.Fatalf("expected TTL %d, got %v", ttl, alice.TTL)
	}
	if len(alice.Tags) != 2 || alice.Tags[0] != "env:prod" || alice.Tags[1] != "scope:read-only" {
		t.Fatalf("unexpected tags: %#v", alice.Tags)
	}

	bob := got.Credentials[1]
	if bob.Key != "sk-bob" {
		t.Fatalf("unexpected key: %q", bob.Key)
	}
	if bob.TTL != nil {
		t.Fatalf("expected no TTL for bob, got %v", bob.TTL)
	}
	if len(bob.Tags) != 0 {
		t.Fatalf("expected no tags for bob, got %#v", bob.Tags)
	}
}

// A model's `labels` map, like every other entity's, must convert into the
// low-level `tags` field on its ai-models entry via labelsToTags.
func TestConvertDBLessModelLabelsToTags(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: prod-model
    capabilities: [generate]
    formats: [{type: openai}]
    labels:
      env: prod
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/prod]}
      model: {alias: prod-model}
  - type: model
    name: plain-model
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/plain]}
      model: {alias: plain-model}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		AIModels []struct {
			Name string   `yaml:"name"`
			Tags []string `yaml:"tags"`
		} `yaml:"ai_models"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(got.AIModels) != 2 {
		t.Fatalf("expected 2 ai_models, got %d: %s", len(got.AIModels), out)
	}

	prod := got.AIModels[0]
	if prod.Name != "prod-model" {
		t.Fatalf("unexpected name: %q", prod.Name)
	}
	if len(prod.Tags) != 1 || prod.Tags[0] != "env:prod" {
		t.Fatalf("unexpected tags: %#v", prod.Tags)
	}

	plain := got.AIModels[1]
	if plain.Name != "plain-model" {
		t.Fatalf("unexpected name: %q", plain.Name)
	}
	if len(plain.Tags) != 0 {
		t.Fatalf("expected no tags for plain-model, got %#v", plain.Tags)
	}
}

func TestConvertDBLessMCPLabelsBecomePluginTags(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-only
    name: team-a
    labels:
      aigw610: tools
    config:
      route: {paths: [/mcp/team-a]}
      url: https://team-a.internal.example.com/mcp
      tools:
        - name: team-a-get-report
          description: Get a report
          method: GET
          path: /report
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      sources: [team-a]
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Services []struct {
			Name string   `yaml:"name"`
			Tags []string `yaml:"tags"`
		} `yaml:"services"`
		Plugins []struct {
			Name   string         `yaml:"name"`
			Tags   []string       `yaml:"tags"`
			Config map[string]any `yaml:"config"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	var teamAServiceTags []string
	for _, service := range got.Services {
		if service.Name == "team-a" {
			teamAServiceTags = service.Tags
			break
		}
	}
	if len(teamAServiceTags) != 1 || teamAServiceTags[0] != "aigw610:tools" {
		t.Fatalf("unexpected team-a service tags: %#v", teamAServiceTags)
	}

	var conversionOnlyTags []string
	for _, plugin := range got.Plugins {
		if plugin.Name != "ai-mcp-proxy" {
			continue
		}
		if plugin.Config["mode"] == "conversion-only" {
			conversionOnlyTags = plugin.Tags
		}
	}

	if len(conversionOnlyTags) != 1 || conversionOnlyTags[0] != "aigw610:tools" {
		t.Fatalf("unexpected conversion-only plugin tags: %#v", conversionOnlyTags)
	}
}

// TestConvertMCPListenerSourcesTagSourcePlugins asserts the listener/source
// relationship: a listener's server.tag (set by the CP from the listener id) is
// carried through to its plugin and attached to every referenced source's
// ai-mcp-proxy plugin tags, so the DP exposes exactly those sources' tools on
// the listener. A source referenced by two listeners accumulates both tags.
func TestConvertMCPListenerSourcesTagSourcePlugins(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-only
    name: team-a
    labels:
      team: a
    config:
      route: {paths: [/mcp/team-a]}
      url: https://team-a.internal.example.com/mcp
      tools:
        - {name: team-a-report, description: Get a report, method: GET, path: /report}
  - type: upstream-server
    name: team-b
    config:
      route: {paths: [/mcp/team-b]}
      url: https://team-b.internal.example.com/mcp
      tools_cache_ttl_seconds: 0
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      server:
        tag: mcp-listener:aggregate-id
      sources: [team-a, team-b]
  - type: listener
    name: aggregate-2
    config:
      route: {paths: [/mcp/aggregate-2]}
      server:
        tag: mcp-listener:aggregate-2-id
      sources: [team-a]
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Services []struct {
			ID   string `yaml:"id"`
			Name string `yaml:"name"`
		} `yaml:"services"`
		Routes []struct {
			ID      string `yaml:"id"`
			Service struct {
				ID string `yaml:"id"`
			} `yaml:"service"`
		} `yaml:"routes"`
		Plugins []struct {
			Name   string         `yaml:"name"`
			Tags   []string       `yaml:"tags"`
			Config map[string]any `yaml:"config"`
			Route  struct {
				ID string `yaml:"id"`
			} `yaml:"route"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// Resolve each plugin's MCP server name via route.id -> route.service.id ->
	// service.name (db-less output references entities by id, not name).
	serviceNameByID := map[string]string{}
	for _, s := range got.Services {
		serviceNameByID[s.ID] = s.Name
	}
	serviceByRouteID := map[string]string{}
	for _, r := range got.Routes {
		serviceByRouteID[r.ID] = serviceNameByID[r.Service.ID]
	}

	pluginTagsByService := map[string][]string{}
	serverTagByService := map[string]string{}
	for _, plugin := range got.Plugins {
		if plugin.Name != "ai-mcp-proxy" {
			continue
		}
		name := serviceByRouteID[plugin.Route.ID]
		pluginTagsByService[name] = plugin.Tags
		if server, ok := plugin.Config["server"].(map[string]any); ok {
			serverTagByService[name], _ = server["tag"].(string)
		}
	}
	aggregateServerTag := serverTagByService["aggregate"]
	aggregate2ServerTag := serverTagByService["aggregate-2"]

	// Listener plugins keep their own server.tag bucket selector.
	require.Equal(t, "mcp-listener:aggregate-id", aggregateServerTag)
	require.Equal(t, "mcp-listener:aggregate-2-id", aggregate2ServerTag)

	// team-a is referenced by both listeners, so it accumulates both tags (plus
	// its own label tag), sorted for determinism.
	require.Equal(t, []string{
		"mcp-listener:aggregate-2-id",
		"mcp-listener:aggregate-id",
		"team:a",
	}, pluginTagsByService["team-a"])

	// team-b is referenced only by the first listener.
	require.Equal(t, []string{"mcp-listener:aggregate-id"}, pluginTagsByService["team-b"])

	// Listeners are not themselves sources, so they carry no listener tags.
	require.Empty(t, pluginTagsByService["aggregate"])
	require.Empty(t, pluginTagsByService["aggregate-2"])
}

// TestConvertMCPListenerSourcesIgnoreServerLabel asserts that a listener
// declaring its bucket selector under the deprecated server.label no longer
// wires sources — only server.tag is honored.
func TestConvertMCPListenerSourcesIgnoreServerLabel(t *testing.T) {
	src := []byte(`
mcp_servers:
  - type: conversion-only
    name: team-a
    config:
      route: {paths: [/mcp/team-a]}
      url: https://team-a.internal.example.com/mcp
      tools:
        - {name: team-a-report, description: Get a report, method: GET, path: /report}
  - type: listener
    name: aggregate
    config:
      route: {paths: [/mcp/aggregate]}
      server:
        label: mcp-listener:aggregate-id
      sources: [team-a]
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Services []struct {
			ID   string `yaml:"id"`
			Name string `yaml:"name"`
		} `yaml:"services"`
		Routes []struct {
			ID      string `yaml:"id"`
			Service struct {
				ID string `yaml:"id"`
			} `yaml:"service"`
		} `yaml:"routes"`
		Plugins []struct {
			Name  string   `yaml:"name"`
			Tags  []string `yaml:"tags"`
			Route struct {
				ID string `yaml:"id"`
			} `yaml:"route"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	serviceNameByID := map[string]string{}
	for _, s := range got.Services {
		serviceNameByID[s.ID] = s.Name
	}
	serviceByRouteID := map[string]string{}
	for _, r := range got.Routes {
		serviceByRouteID[r.ID] = serviceNameByID[r.Service.ID]
	}

	var teamATags []string
	for _, plugin := range got.Plugins {
		if plugin.Name != "ai-mcp-proxy" {
			continue
		}
		if serviceByRouteID[plugin.Route.ID] == "team-a" {
			teamATags = plugin.Tags
		}
	}

	require.Nil(t, teamATags)
}

func TestConvertScopedPoliciesDoNotReusePolicyID(t *testing.T) {
	src := []byte(`
policies:
  - id: policy-123
    name: sanitizer
    type: ai-sanitizer
    config:
      host: localhost
models:
  - type: model
    name: model-one
    capabilities: [generate]
    formats: [{type: openai}]
    policies: [sanitizer]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/model-one]}
      model: {alias: model-one}
  - type: api
    name: api-one
    capabilities: [files]
    formats: [{type: openai}]
    policies: [sanitizer]
    targets:
      - name: files-api
        provider: p1
        config: {type: openai}
    config:
      route: {paths: [/api-one]}
      model: {name_header: false}
model_providers:
  - name: p1
    type: openai
`)

	out, _, err := encodeDBLessYAML(src, testOptions)
	if err != nil {
		t.Fatalf("convert db-less: %v", err)
	}

	var got struct {
		Plugins []struct {
			ID    string `yaml:"id"`
			Name  string `yaml:"name"`
			Route struct {
				ID string `yaml:"id"`
			} `yaml:"route"`
			Model struct {
				ID string `yaml:"id"`
			} `yaml:"model"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	var sanitizerCount int
	seenIDs := map[string]bool{}
	for _, plugin := range got.Plugins {
		if plugin.Name != "ai-sanitizer" {
			continue
		}
		sanitizerCount++
		if plugin.ID == "" {
			t.Fatalf("scoped ai-sanitizer plugin missing ID: %#v", plugin)
		}
		if plugin.ID == "policy-123" {
			t.Fatalf("scoped ai-sanitizer plugin unexpectedly preserved source policy ID %q", plugin.ID)
		}
		if seenIDs[plugin.ID] {
			t.Fatalf("scoped ai-sanitizer plugins unexpectedly reused generated ID %q", plugin.ID)
		}
		seenIDs[plugin.ID] = true
		if plugin.Route.ID == "" {
			t.Fatalf("scoped ai-sanitizer plugin missing route ref: %#v", plugin)
		}
	}

	if sanitizerCount != 2 {
		t.Fatalf("expected 2 scoped ai-sanitizer plugins, got %d: %s", sanitizerCount, out)
	}
}

// TestGlobalPolicyRejectsAuthTypeOnRealtimeDocument pins that a global
// authentication policy stays and the WebSocket model fails closed.
func TestGlobalPolicyRejectsAuthTypeOnRealtimeDocument(t *testing.T) {
	result, err := Convert([]byte(`
policies:
  - type: key-auth
    name: require-key
    global: true
models:
  - name: m
    capabilities: [realtime]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
  - name: chat
    capabilities: [generate]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`+overrideProvider), testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	issue := requireIssue(t, result.Issues, Error, ConfigFieldInvalid, kindModel, "m", "capabilities[0]")
	require.Contains(t, issue.Reason, `global policy "require-key" of type "key-auth"`)
	require.Equal(t, "key-auth", result.Output.Plugins[0].Name, "the global policy stays")
	require.Len(t, result.Output.AIModels, 1, "the WebSocket model is left out")
	require.Equal(t, "chat", result.Output.AIModels[0].Name)
}

// TestGlobalPolicyWarnsOnRealtimeDocument pins that a non-auth global policy
// only warns, since it still protects every HTTP route; it just never runs
// against the document's WebSocket traffic.
func TestGlobalPolicyWarnsOnRealtimeDocument(t *testing.T) {
	src := []byte(`
policies:
  - type: http-log
    name: audit-log
    global: true
    config: {http_endpoint: https://logs.example.com/ingest}
models:
  - name: m
    capabilities: [realtime]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
` + overrideProvider)
	_, warnings, err := convertDoc(src, false)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `global policy "audit-log" (type "http-log")`)
	require.Contains(t, warnings[0], "will not run on the WebSocket routes")

	_, _, err = convertDoc(src, true)
	require.Error(t, err)
}

// TestGlobalPolicyNoWarningWithoutRealtimeRoute pins that global policies are
// unaffected when the document emits no WebSocket route.
func TestGlobalPolicyNoWarningWithoutRealtimeRoute(t *testing.T) {
	_, warnings, err := convertDoc([]byte(`
policies:
  - type: key-auth
    name: require-key
    global: true
models:
  - name: m
    capabilities: [generate]
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
`+overrideProvider), true)
	require.NoError(t, err)
	require.Empty(t, warnings)
}
