package revert

import (
	"fmt"
	"testing"

	"github.com/Kong/ai-deck-converter/aigw"
	"github.com/Kong/ai-deck-converter/convert"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

const clonedTransformerModel = `
models:
  - type: model
    name: transformed
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

// A cloned response transformer the converter emits to reorder a policy is
// recovered as the policy type it clones: the AI Gateway model has no clone.
func TestRevertClonedResponseTransformerBecomesPolicy(t *testing.T) {
	for _, tc := range []struct {
		policyType string
		policyName string
		policyDef  string
	}{
		{"response-transformer", "plain", `
  - type: response-transformer
    name: plain
    config:
      remove:
        json: [service_tier]
`},
		{"response-transformer-advanced", "adv", `
  - type: response-transformer-advanced
    name: adv
    config:
      add:
        json: ["added:yes"]
`},
	} {
		t.Run(tc.policyType, func(t *testing.T) {
			src := []byte(fmt.Sprintf(clonedTransformerModel, tc.policyName, tc.policyDef))
			converted, err := convert.Convert(src, convert.Options{
				MinRuntimeVersion: aimap.LatestVersion.String(),
			})
			require.NoError(t, err)
			require.Len(t, converted.Output.ClonedPlugins, 1, "the converter must clone the transformer")

			deck, err := converted.Output.ToYAML()
			require.NoError(t, err)

			aigwYAML, warnings, err := Revert(deck, Options{})
			require.NoError(t, err)
			require.Empty(t, warnings, "revert must not warn about a converter-emitted clone")

			out, err := aigw.Parse(aigwYAML)
			require.NoError(t, err)
			require.Len(t, out.Models, 1)
			require.Equal(t, []string{tc.policyType}, out.Models[0].Policies,
				"the clone must revert to the plugin type it copies")
			require.Len(t, out.Policies, 1)
			require.Equal(t, tc.policyType, out.Policies[0].Type)
		})
	}
}

// A clone the converter does not emit comes back as a policy of the clone name
// and produces a warning, so the loss is visible instead of silent.
func TestRevertWarnsUserAuthoredClone(t *testing.T) {
	priority := 900
	ref := "response-transformer"
	doc := &kong.Document{
		ClonedPlugins: []kong.ClonedPlugin{{
			Name:     "my-transformer",
			Ref:      ref,
			Priority: &priority,
		}},
		Plugins: []kong.Plugin{{
			Name:   "my-transformer",
			Config: map[string]any{"add": map[string]any{"json": []any{"a:1"}}},
		}},
	}

	out, warnings, err := RevertDocument(doc, Options{})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `"my-transformer"`)
	require.Contains(t, warnings[0], "no AI Gateway representation")
	require.Len(t, out.Policies, 1)
	// The policy keeps the plugin it copies; only the clone itself is lossy.
	require.Equal(t, "response-transformer", out.Policies[0].Type)
}

// Any clone is lossy: the model has no cloned_plugins, so the clone name and
// priority are dropped. The policy keeps the plugin the clone copies.
func TestRevertWarnsCloneWithoutModelRepresentation(t *testing.T) {
	doc := &kong.Document{
		ClonedPlugins: []kong.ClonedPlugin{{Name: "my-acl", Ref: "acl"}},
		Plugins:       []kong.Plugin{{Name: "my-acl", Config: map[string]any{"allow": []any{"a"}}}},
	}

	out, warnings, err := RevertDocument(doc, Options{})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "no AI Gateway representation")
	require.Len(t, out.Policies, 1)
	require.Equal(t, "acl", out.Policies[0].Type)
}

// A converter-emitted clone whose priority was edited warns, since converting
// again rewrites it to the converter's own priority.
func TestRevertWarnsConverterCloneWithForeignPriority(t *testing.T) {
	priority := 900
	doc := &kong.Document{
		ClonedPlugins: []kong.ClonedPlugin{{
			Name:     aimap.ResponseTransformerCloneName,
			Ref:      "response-transformer",
			Priority: &priority,
		}},
		Plugins: []kong.Plugin{{
			Name:   aimap.ResponseTransformerCloneName,
			Config: map[string]any{"add": map[string]any{"json": []any{"a:1"}}},
		}},
	}

	_, warnings, err := RevertDocument(doc, Options{})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "priority 900")
	require.Contains(t, warnings[0], "766")
}

// A clone that takes a converter clone name but sets no priority or a different
// ref is not the one the converter emits, so the next conversion would rewrite
// it. Revert warns instead of dropping it silently.
func TestRevertWarnsConverterCloneNameWithWrongShape(t *testing.T) {
	for name, clone := range map[string]kong.ClonedPlugin{
		"no priority": {Name: aimap.ResponseTransformerCloneName, Ref: "response-transformer"},
		"wrong ref": {
			Name:     aimap.ResponseTransformerCloneName,
			Ref:      "response-transformer-advanced",
			Priority: intPtrValue(aimap.ResponseTransformerClonePriority),
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := &kong.Document{
				ClonedPlugins: []kong.ClonedPlugin{clone},
				Plugins: []kong.Plugin{{
					Name:   clone.Name,
					Config: map[string]any{"add": map[string]any{"json": []any{"a:1"}}},
				}},
			}

			_, warnings, err := RevertDocument(doc, Options{})
			require.NoError(t, err)
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], "converting again emits")
		})
	}
}

func intPtrValue(v int) *int { return &v }

// A cloned acl plugin reverts as ACLs, not as a policy, because the type
// resolution happens before the dispatch.
func TestRevertClonedACLBecomesACLs(t *testing.T) {
	doc := &kong.Document{
		ClonedPlugins: []kong.ClonedPlugin{{Name: "my-acl", Ref: "acl"}},
		Plugins: []kong.Plugin{{
			Name:   "my-acl",
			Route:  kong.NewStringRef("r1"),
			Config: map[string]any{"allow": []any{"a"}},
		}},
		Services: []kong.Service{{
			Name:   "svc",
			URL:    "http://upstream.example.com",
			Routes: []kong.Route{{Name: "r1", Paths: []string{"/x"}}},
		}},
	}

	out, warnings, err := RevertDocument(doc, Options{})
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	require.Empty(t, out.Policies, "an acl clone must not become a policy")
	require.Len(t, out.Agents, 1)
	require.Equal(t, []string{"a"}, out.Agents[0].Access.ACLs.Allow)
}

// A cloned auth plugin reverts as its base plugin type. Using the clone name as
// the auth strategy type would produce config that does not convert again.
func TestRevertClonedAuthPluginUsesBaseType(t *testing.T) {
	for _, base := range []string{"key-auth", "openid-connect"} {
		t.Run(base, func(t *testing.T) {
			cfg := map[string]any{"anonymous": anonymousConsumerName}
			if base == "openid-connect" {
				cfg["issuer"] = "https://idp.example.com"
			}
			doc := &kong.Document{
				ClonedPlugins: []kong.ClonedPlugin{{Name: "my-" + base, Ref: base}},
				Plugins: []kong.Plugin{{
					Name:   "my-" + base,
					Route:  kong.NewStringRef("r1"),
					Config: cfg,
				}},
				Services: []kong.Service{{
					Name:   "svc",
					URL:    "http://upstream.example.com",
					Routes: []kong.Route{{Name: "r1", Paths: []string{"/x"}}},
				}},
			}

			out, warnings, err := RevertDocument(doc, Options{})
			require.NoError(t, err)
			require.NotEmpty(t, warnings, "the clone itself is not representable")
			require.Len(t, out.AuthStrategies, 1)
			require.Equal(t, base, out.AuthStrategies[0].Type,
				"the clone must revert as the plugin it copies")
			require.NotContains(t, out.AuthStrategies[0].Config, "anonymous",
				"the synthesized fallback must be stripped")
			require.Empty(t, out.Policies, "an auth clone must not become a policy")
			require.Len(t, out.Agents, 1)
			require.Equal(t, []string{out.AuthStrategies[0].Name}, out.Agents[0].Access.AuthStrategies)
		})
	}
}
