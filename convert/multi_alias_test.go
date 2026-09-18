package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const multiAliasSrc = `
model_providers:
  - name: openai-provider
    type: openai
models:
  - name: multi-alias-model
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config:
      route:
        paths: [/ai]
        model:
          values:
            - "@kong/openai"
            - "@kong/the-openai-model"
            - "@kong/openai-model"
            - "@kong/chat-gpt"
    targets:
      - name: gpt-5
        provider: openai-provider
        config: {type: openai}
`

func TestConvertMultiAliasValuesFanOut(t *testing.T) {
	out, warnings, err := Convert([]byte(multiAliasSrc), Options{})
	require.NoError(t, err, "convert")
	require.Empty(t, warnings, "no warnings expected")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	aliases := []string{
		"@kong/openai",
		"@kong/the-openai-model",
		"@kong/openai-model",
		"@kong/chat-gpt",
	}

	// One ai-models entry per alias, in authored order. The entries carry a
	// marker tag identifying the fan-out group (keyed by the first alias) so
	// revert can tell them apart from independently-authored models.
	aiModels, ok := got["ai_models"].([]any)
	require.True(t, ok, "expected ai_models collection")
	require.Len(t, aiModels, len(aliases), "one ai_models entry per alias value")
	for i, raw := range aiModels {
		entry, ok := raw.(map[string]any)
		require.True(t, ok, "expected ai_models entry")
		require.Equal(t, aliases[i], entry["name"], "ai_models entry %d name", i)
		_, hasAlias := entry["alias"]
		require.False(t, hasAlias, "ai_models alias should be unset")
		tags := entryTags(t, entry)
		require.Contains(t, tags, "ai-gateway-model-alias-group:@kong/openai",
			"ai_models entry %d must carry the fan-out group marker", i)
	}

	// One ai-proxy-advanced per alias, each scoped to its own ai-model FK with
	// the targets' model_alias set to that alias. The single shared route and
	// its ai-model-selector are emitted once.
	var proxies []map[string]any
	selectors := 0
	plugins, ok := got["plugins"].([]any)
	require.True(t, ok, "expected plugins collection")
	for _, raw := range plugins {
		plugin := raw.(map[string]any)
		switch plugin["name"] {
		case "ai-proxy-advanced":
			proxies = append(proxies, plugin)
		case "ai-model-selector":
			selectors++
		}
	}
	require.Len(t, proxies, len(aliases), "one ai-proxy-advanced per alias")
	require.Equal(t, 1, selectors, "one shared ai-model-selector")

	for i, proxy := range proxies {
		require.Equal(t, aliases[i], proxy["model"],
			"ai-proxy-advanced %d must scope to alias %q", i, aliases[i])
		cfg := proxy["config"].(map[string]any)
		targets := cfg["targets"].([]any)
		require.Len(t, targets, 1, "ai-proxy-advanced %d targets", i)
		model := targets[0].(map[string]any)["model"].(map[string]any)
		require.Equal(t, aliases[i], model["model_alias"],
			"ai-proxy-advanced %d target model_alias", i)
		require.Equal(t, "gpt-5", model["name"], "ai-proxy-advanced %d target name", i)
	}
}

func TestConvertMultiAliasValuesPoliciesFanOut(t *testing.T) {
	src := `
model_providers:
  - name: openai-main
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "{vault://env/openai-key}"}]
policies:
  - type: ai-prompt-guard
    name: pii-sanitizer
    config:
      allow_patterns: ["^safe"]
models:
  - type: model
    name: guarded-gpt
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: openai-main
        config: {type: openai}
    policies: [pii-sanitizer]
    access:
      acls:
        allow: [premium-users]
    config:
      route:
        paths: [/ai]
        model:
          values: ["@kong/gpt-4o", "@kong/gpt-4o-alias"]
`
	out, warnings, err := Convert([]byte(src), Options{})
	require.NoError(t, err, "convert")
	require.Empty(t, warnings, "no warnings expected")

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got), "unmarshal output")

	// A model-scoped policy/ACL must protect requests regardless of which
	// alias they name: one copy of each guard plugin per alias.
	aliases := []string{"@kong/gpt-4o", "@kong/gpt-4o-alias"}
	counts := map[string][]string{}
	plugins := got["plugins"].([]any)
	for _, raw := range plugins {
		plugin := raw.(map[string]any)
		name, _ := plugin["name"].(string)
		switch name {
		case "ai-prompt-guard", "acl":
			fk, _ := plugin["model"].(string)
			counts[name] = append(counts[name], fk)
		}
	}
	require.ElementsMatch(t, aliases, counts["ai-prompt-guard"],
		"one ai-prompt-guard copy per alias")
	require.ElementsMatch(t, aliases, counts["acl"],
		"one acl copy per alias")
}

// entryTags reads the tags list off a decoded ai_models entry.
func entryTags(t *testing.T, entry map[string]any) []string {
	t.Helper()
	raw, ok := entry["tags"].([]any)
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(raw))
	for _, r := range raw {
		s, ok := r.(string)
		require.True(t, ok, "expected string tag")
		tags = append(tags, s)
	}
	return tags
}
