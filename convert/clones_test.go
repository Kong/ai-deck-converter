package convert

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

const transformerModel = `
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [%s]
    config:
      route:
        paths: [/ai]
        model: {values: [alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
%s
`

func transformerModelConfig(policies, policyDefs string) []byte {
	return []byte(fmt.Sprintf(transformerModel, policies, policyDefs))
}

func TestConvertClonesModelScopedResponseTransformers(t *testing.T) {
	for _, tc := range []struct {
		policyType string
		cloneName  string
	}{
		{"response-transformer", aimap.ResponseTransformerCloneName},
		{"response-transformer-advanced", aimap.ResponseTransformerAdvancedCloneName},
	} {
		t.Run(tc.policyType, func(t *testing.T) {
			src := transformerModelConfig(tc.policyType, fmt.Sprintf(`
  - type: %s
    name: %s
    config:
      remove:
        json: [service_tier]
`, tc.policyType, tc.policyType))

			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			// The plain transformer also gets the documented gzip warning.
			for _, issue := range result.Issues {
				require.Equal(t, Warning, issue.Severity)
				require.Equal(t, BehaviorMayDiffer, issue.Code)
			}

			require.Len(t, result.Output.ClonedPlugins, 1)
			clone := result.Output.ClonedPlugins[0]
			require.Equal(t, tc.cloneName, clone.Name)
			require.Equal(t, tc.policyType, clone.Ref)
			require.NotNil(t, clone.Priority)
			require.Equal(t, aimap.ResponseTransformerClonePriority, *clone.Priority)
			require.Less(t, *clone.Priority, aimap.PluginPriority["ai-proxy-advanced"],
				"the clone must run after ai-proxy-advanced")

			names := allPluginNames(result.Output)
			require.Contains(t, names, tc.cloneName)
			require.NotContains(t, names, tc.policyType)
		})
	}
}

// A plain response-transformer whose body operations cannot decode the gzip
// body that ai-proxy-advanced produces is reported, and still cloned so the
// header change and a non-gzip client's body change both work.
func TestConvertWarnsPlainResponseTransformerOnCompressedBodies(t *testing.T) {
	src := transformerModelConfig("plain", `
  - type: response-transformer
    name: plain
    config:
      remove:
        json: [service_tier]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, kindModel, "guarded", "policies[0]")
	require.Len(t, result.Output.ClonedPlugins, 1)
	require.Equal(t, aimap.ResponseTransformerCloneName, result.Output.ClonedPlugins[0].Name)
}

// A headers-only response-transformer needs no gzip caveat.
func TestConvertDoesNotWarnHeadersOnlyResponseTransformer(t *testing.T) {
	src := transformerModelConfig("plain", `
  - type: response-transformer
    name: plain
    config:
      add:
        headers: ["X-Added:av"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
}

// Below the cloning gate the policy keeps the plugin's static priority, so its
// body change is lost. The converter says so instead of emitting a config the
// data plane rejects.
func TestConvertWarnsResponseTransformerBelowClonedPluginsGate(t *testing.T) {
	src := transformerModelConfig("adv", `
  - type: response-transformer-advanced
    name: adv
    config:
      replace:
        body: '{}'
`)

	result, err := Convert(src, Options{MinRuntimeVersion: "2.0"})
	require.NoError(t, err)
	requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, kindModel, "guarded", "policies[0]")
	require.Empty(t, result.Output.ClonedPlugins)
	require.Contains(t, allPluginNames(result.Output), "response-transformer-advanced")
}

// A global response transformer is one instance for the whole workspace, so
// moving it would reorder it against unrelated plugins too.
func TestConvertWarnsGlobalResponseTransformer(t *testing.T) {
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
    config:
      route:
        paths: [/ai]
        model: {values: [alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    global: true
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	issue := requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, kindPolicy, "adv", "global")
	require.Contains(t, issue.Reason, "does not reorder a global plugin")
	require.Empty(t, result.Output.ClonedPlugins)
	require.Contains(t, allPluginNames(result.Output), "response-transformer-advanced")
}

// An Agent-scoped transformer already runs after ai-a2a-proxy, so it needs no
// clone.
func TestConvertDoesNotCloneAgentResponseTransformer(t *testing.T) {
	src := []byte(`
agents:
  - name: agent
    type: a2a
    policies: [adv]
    config:
      url: http://agent.example.com
      route: {paths: [/agent]}
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Output.ClonedPlugins)
	require.Contains(t, allPluginNames(result.Output), "response-transformer-advanced")
}

// A type "api" model never buffers and normalizes a response body, so
// ai-proxy-advanced does not rewrite it and the transformer keeps its static
// priority.
func TestConvertDoesNotCloneAPIModelResponseTransformer(t *testing.T) {
	src := []byte(`
models:
  - type: api
    name: files-api
    capabilities: [files]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route:
        paths: [/files-api]
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Output.ClonedPlugins)
	require.Contains(t, allPluginNames(result.Output), "response-transformer-advanced")
}

// The db-less layout carries the clone definitions too, with stable IDs.
func TestConvertEmitsClonedPluginsInDBLessOutput(t *testing.T) {
	src := transformerModelConfig("adv", `
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)

	out := result.Output.ToDBLess()
	require.Len(t, out.ClonedPlugins, 1)
	clone := out.ClonedPlugins[0]
	require.NotEmpty(t, clone.ID)
	require.Equal(t, aimap.ResponseTransformerAdvancedCloneName, clone.Name)
	require.Equal(t, "response-transformer-advanced", clone.Ref)
	require.NotNil(t, clone.Priority)
	require.Equal(t, aimap.ResponseTransformerClonePriority, *clone.Priority)
}

// allPluginNames returns every plugin name in the output, at any scope.
func allPluginNames(doc *kong.Document) []string {
	var names []string
	for _, p := range doc.Plugins {
		names = append(names, p.Name)
	}
	for _, svc := range doc.Services {
		for _, p := range svc.Plugins {
			names = append(names, p.Name)
		}
		for _, route := range svc.Routes {
			for _, p := range route.Plugins {
				names = append(names, p.Name)
			}
		}
	}
	for _, route := range doc.Routes {
		for _, p := range route.Plugins {
			names = append(names, p.Name)
		}
	}
	return names
}

// A headers-only transformer keeps its static priority: its header change
// already survives, so cloning it would reorder it for no benefit.
func TestConvertDoesNotCloneHeadersOnlyTransformer(t *testing.T) {
	for _, policyType := range []string{"response-transformer", "response-transformer-advanced"} {
		t.Run(policyType, func(t *testing.T) {
			src := transformerModelConfig("headers-only", fmt.Sprintf(`
  - type: %s
    name: headers-only
    config:
      add:
        headers: ["X-Added:av"]
`, policyType))

			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			require.Empty(t, result.Issues)
			require.Empty(t, result.Output.ClonedPlugins)
			require.Contains(t, allPluginNames(result.Output), policyType)
		})
	}
}

// Every body operation the two plugins act on triggers the clone, including the
// advanced plugin's replace.body and allow.json and the plain plugin's
// rename.json.
func TestConvertClonesEveryBodyOperation(t *testing.T) {
	for name, tc := range map[string]struct {
		policyType string
		config     string
	}{
		"replace-body": {"response-transformer-advanced", "      replace:\n        body: '{}'\n"},
		"allow-json":   {"response-transformer-advanced", "      allow:\n        json: [keep]\n"},
		"transform-functions": {"response-transformer-advanced", "      transform:\n" +
			"        functions:\n          - \"return function(v) return v end\"\n"},
		"rename-json": {"response-transformer", "      rename:\n        json: [\"object:kind\"]\n"},
		"remove-json": {"response-transformer", "      remove:\n        json: [service_tier]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			src := transformerModelConfig("adv", fmt.Sprintf(`
  - type: %s
    name: adv
    config:
%s`, tc.policyType, tc.config))

			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			// The plain transformer also warns about gzip-accepting clients.
			for _, issue := range result.Issues {
				require.Equal(t, BehaviorMayDiffer, issue.Code)
			}
			require.Len(t, result.Output.ClonedPlugins, 1,
				"a body operation must clone the transformer")
			require.Equal(t, tc.policyType, result.Output.ClonedPlugins[0].Ref)
		})
	}
}

// transform.json only scopes transform.functions, so on its own it changes no
// body and needs no clone.
func TestConvertDoesNotCloneTransformJSONOnly(t *testing.T) {
	src := transformerModelConfig("adv", `
  - type: response-transformer-advanced
    name: adv
    config:
      transform:
        json: [keep]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Output.ClonedPlugins)
	require.Contains(t, allPluginNames(result.Output), "response-transformer-advanced",
		"the policy must still be emitted")
}

// A model with a chat route and a realtime route keeps the transformer on the
// chat route only: the clone runs there, the realtime route drops the plugin
// under the name the user wrote, and the output holds one clone definition.
func TestConvertClonesTransformerOnlyOnTheHTTPRoute(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: mixed
    capabilities: [generate, realtime]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route:
        paths: [/mixed]
        model: {values: [mixed-alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Output.ClonedPlugins, 1)

	// The drop names the plugin the user wrote, not the clone.
	var dropped bool
	for _, issue := range result.Issues {
		if issue.Code == ConfigFieldDropped && strings.Contains(issue.Reason, `"response-transformer-advanced"`) {
			dropped = true
		}
	}
	require.True(t, dropped, "the realtime route must report the dropped transformer: %v", result.Issues)

	var clonedPlugin, plainPlugin int
	for _, p := range result.Output.Plugins {
		switch p.Name {
		case aimap.ResponseTransformerAdvancedCloneName:
			clonedPlugin++
		case "response-transformer-advanced":
			plainPlugin++
		}
	}
	require.Equal(t, 1, clonedPlugin, "the chat route gets the clone")
	require.Equal(t, 0, plainPlugin, "no route keeps the static-priority transformer")
}

// Two models on one route share the one clone definition.
func TestConvertSharesOneCloneAcrossModels(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: first
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route:
        paths: [/ai]
        model: {values: [first-alias]}
  - type: model
    name: second
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route:
        paths: [/ai]
        model: {values: [second-alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Output.ClonedPlugins, 1, "one clone serves both models")

	var cloned int
	for _, p := range result.Output.Plugins {
		if p.Name == aimap.ResponseTransformerAdvancedCloneName {
			cloned++
		}
	}
	require.Equal(t, 2, cloned, "one clone instance per model")
}

// A multi-alias model gets one clone instance per alias, like every other
// model-scoped plugin, and still one clone definition.
func TestConvertClonesTransformerForEveryAlias(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: multi
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route:
        paths: [/ai]
        model: {values: [alias-one, alias-two]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Output.ClonedPlugins, 1)

	models := map[string]bool{}
	for _, p := range result.Output.Plugins {
		if p.Name == aimap.ResponseTransformerAdvancedCloneName && p.Model != nil {
			models[string(*p.Model)] = true
		}
	}
	require.Equal(t, map[string]bool{"alias-one": true, "alias-two": true}, models)
}

// The video lifecycle route carries the model's policies too, so it clones the
// transformer the same way.
func TestConvertClonesTransformerOnVideoLifecycleRoute(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: sora
    capabilities: [video]
    formats: [{type: openai}]
    targets:
      - name: sora-2
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route:
        paths: [/ai]
        model: {values: [sora-alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Output.ClonedPlugins, 1)

	var lifecycle int
	for _, p := range result.Output.Plugins {
		if p.Name != aimap.ResponseTransformerAdvancedCloneName || p.Route == nil {
			continue
		}
		if strings.Contains(string(*p.Route), "videos-lifecycle") {
			lifecycle++
		}
	}
	require.Equal(t, 1, lifecycle, "the video lifecycle route carries the cloned transformer")
}

// The responses lifecycle route carries its own ai-proxy-advanced, so it needs
// the same clone the create route gets. Its guards are route-scoped because no
// ai-model-selector runs there.
func TestConvertClonesTransformerOnResponsesLifecycleRoute(t *testing.T) {
	src := []byte(`
models:
  - name: guarded
    type: model
    capabilities: [generate, agentic]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route: {paths: [/ai]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Len(t, result.Output.ClonedPlugins, 1)

	clonedOn := map[string]bool{}
	for _, p := range result.Output.Plugins {
		if p.Name != aimap.ResponseTransformerAdvancedCloneName || p.Route == nil {
			continue
		}
		clonedOn[string(*p.Route)] = true
	}
	require.True(t, clonedOn["openai-responses-lifecycle"],
		"the lifecycle route must run the clone, not the static-priority plugin")
	require.True(t, clonedOn["openai-responses"], "the create route must run the clone")

	for _, p := range result.Output.Plugins {
		if p.Name == "response-transformer-advanced" && p.Route != nil {
			t.Fatalf("route %q kept the static-priority transformer, so ai-proxy-advanced overwrites its body",
				*p.Route)
		}
	}
}

// A model whose lifecycle route shares a route with another model still
// fingerprints the same policy set, whether or not it is cloned, so the shared
// route does not warn about differing policies.
func TestConvertLifecycleGuardsFingerprintAcrossCloneStates(t *testing.T) {
	src := []byte(`
models:
  - name: first
    type: model
    capabilities: [generate, agentic]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route: {paths: [/ai]}
  - name: second
    type: model
    capabilities: [generate, agentic]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [adv]
    config:
      route: {paths: [/ai]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	for _, issue := range result.Issues {
		require.NotEqual(t, BehaviorMayDiffer, issue.Code,
			"the two models configure the same policy, so sharing the lifecycle route must not warn: %v", issue)
	}
	require.Len(t, result.Output.ClonedPlugins, 1, "one clone definition covers the shared route")
}

// A consumer- or consumer-group-scoped transformer is one instance for all of
// that caller's routes, so the clone cannot reorder it. The loss is reported
// instead of staying silent.
func TestConvertWarnsConsumerScopedResponseTransformer(t *testing.T) {
	for name, tc := range map[string]struct {
		scope  string
		kind   string
		entity string
	}{
		"consumer":       {"consumers:\n  - name: c1\n    type: api-key\n    policies: [adv]\n", kindConsumer, "c1"},
		"consumer group": {"consumer_groups:\n  - name: g1\n    policies: [adv]\n", kindConsumerGroup, "g1"},
	} {
		t.Run(name, func(t *testing.T) {
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
    config:
      route:
        paths: [/ai]
        model: {values: [alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
` + tc.scope)

			result, err := Convert(src, testOptions)
			require.NoError(t, err)
			issue := requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, tc.kind, tc.entity, "policies[0]")
			require.Contains(t, issue.Reason, "cannot change the response body")
			require.Empty(t, result.Output.ClonedPlugins)
		})
	}
}

// A headers-only transformer on a consumer needs no warning: it changes no body.
func TestConvertDoesNotWarnConsumerHeadersOnlyTransformer(t *testing.T) {
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
    config:
      route:
        paths: [/ai]
        model: {values: [alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        headers: ["X-Added:av"]
consumers:
  - name: c1
    type: api-key
    policies: [adv]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
}

// A realtime-only model drops the transformer on every route, so a body-loss
// warning would send the user to a plugin those routes drop too. Only the drop
// is reported.
func TestConvertDoesNotWarnTransformerDroppedOnEveryRoute(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: rt-only
    capabilities: [realtime]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [plain]
    config:
      route:
        paths: [/rt]
        model: {values: [rt-alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer
    name: plain
    config:
      remove:
        json: [service_tier]
`)

	for name, version := range map[string]string{"above gate": "2.3", "below gate": "2.0"} {
		t.Run(name, func(t *testing.T) {
			result, err := Convert(src, Options{MinRuntimeVersion: version})
			require.NoError(t, err)
			require.Empty(t, result.Output.ClonedPlugins)

			var dropped, bodyLoss int
			for _, issue := range result.Issues {
				if issue.Code == ConfigFieldDropped {
					dropped++
				}
				if issue.Code == BehaviorMayDiffer {
					bodyLoss++
				}
			}
			require.Equal(t, 1, dropped, "the WebSocket drop must be reported")
			require.Zero(t, bodyLoss,
				"a plugin no route keeps must not get a body-loss warning: %v", result.Issues)
		})
	}
}

// A model with a chat route keeps the transformer, so the body-loss warning for
// a client that accepts gzip still applies.
func TestConvertStillWarnsTransformerKeptOnAnHTTPRoute(t *testing.T) {
	src := []byte(`
models:
  - type: model
    name: mixed
    capabilities: [generate, realtime]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [plain]
    config:
      route:
        paths: [/mixed]
        model: {values: [mixed-alias]}
model_providers:
  - name: p1
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "Bearer token"}]
policies:
  - type: response-transformer
    name: plain
    config:
      remove:
        json: [service_tier]
`)

	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, kindModel, "mixed", "policies[0]")
	require.Len(t, result.Output.ClonedPlugins, 1)
}
