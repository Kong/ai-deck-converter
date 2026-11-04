package revert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/convert"
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Model y makes the ai_models entry "shared". Revert reads the route of model
// z first, because model x made that route earlier. Model v comes after y and
// before z, so revert gives the entry to y.
func TestRevertSplitsSharedAIModelByRoute(t *testing.T) {
	src := []byte(`
model_providers: [{name: openai, type: openai}]
models:
  - name: x
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
  - name: y
    id: 22222222-2222-2222-2222-222222222222
    labels: {team: y}
    capabilities: [generate]
    targets: [{name: gpt-5-mini, provider: openai}]
    config: {route: {headers: {X-Team: ["y"]}, model: {values: [shared]}}}
  - name: v
    capabilities: [generate]
    targets: [{name: gpt-4o, provider: openai}]
  - name: z
    capabilities: [generate]
    targets: [{name: gpt-5-nano, provider: openai}]
    config: {route: {model: {values: [shared]}}}
  - name: w
    capabilities: [generate]
    targets: [{name: gpt-5-nano, provider: openai}]
    config: {route: {paths: [/other], strip_path: true, model: {values: [shared]}}}
`)
	first, err := convert.Convert(src, latestConvertOptions)
	require.NoError(t, err)
	require.Empty(t, first.Issues)
	deck1, err := first.Output.ToYAML()
	require.NoError(t, err)

	aigwYAML, warnings, err := Revert(deck1, Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)

	var doc aigw.Document
	require.NoError(t, yaml.Unmarshal(aigwYAML, &doc))
	require.Len(t, doc.Models, 5)
	owner := doc.Models[1]
	require.Equal(t, "shared", owner.Name)
	require.Equal(t, "22222222-2222-2222-2222-222222222222", owner.ID)
	require.Equal(t, map[string][]string{"X-Team": {"y"}}, owner.Config.Route.Headers)
	require.Equal(t, "v", doc.Models[2].Name)
	require.Equal(t, []string{"shared"}, doc.Models[3].Config.Route.Model.Values)
	require.Empty(t, doc.Models[3].ID)
	require.Empty(t, doc.Models[3].Config.Route.Headers)
	require.Equal(t, []string{"/other"}, doc.Models[4].Config.Route.Paths)
	require.Equal(t, new(true), doc.Models[4].Config.Route.StripPath)

	second, err := convert.Convert(aigwYAML, latestConvertOptions)
	require.NoError(t, err)
	require.Empty(t, second.Issues)
	deck2, err := second.Output.ToYAML()
	require.NoError(t, err)
	require.Equal(t, string(deck1), string(deck2), "intermediate aigw:\n%s", aigwYAML)
}

// Model y shares the entry foo with model x. Its other aliases keep their
// group, so the round trip gives the same decK config.
func TestRevertSharedAIModelMultiAlias(t *testing.T) {
	for name, src := range map[string]string{
		"one owned entry": `
model_providers: [{name: openai, type: openai}]
models:
  - name: x
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/x], model: {values: [foo, baz]}}}
  - name: y
    id: 22222222-2222-2222-2222-222222222222
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/y], model: {values: [foo, bar]}}}
`,
		"two owned entries": `
model_providers: [{name: openai, type: openai}]
models:
  - name: x
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/x], model: {values: [foo]}}}
  - name: y
    id: 22222222-2222-2222-2222-222222222222
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/y], model: {values: [foo, bar, baz]}}}
`,
		"empty ID filled": `
model_providers: [{name: openai, type: openai}]
models:
  - name: a
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/a], model: {values: [gpt]}}}
  - name: b
    id: 22222222-2222-2222-2222-222222222222
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/b], model: {values: [gpt]}}}
`,
	} {
		t.Run(name, func(t *testing.T) {
			first, err := convert.Convert([]byte(src), latestConvertOptions)
			require.NoError(t, err)
			deck1, err := first.Output.ToYAML()
			require.NoError(t, err)

			aigwYAML, warnings, err := Revert(deck1, Options{})
			require.NoError(t, err)
			require.Empty(t, warnings)

			second, err := convert.Convert(aigwYAML, latestConvertOptions)
			require.NoError(t, err)
			require.Empty(t, second.Issues)
			deck2, err := second.Output.ToYAML()
			require.NoError(t, err)
			require.Equal(t, string(deck1), string(deck2), "intermediate aigw:\n%s", aigwYAML)
		})
	}
}

// Models a and b share the entry s on two routes that differ only in methods.
// The route of a uses the default methods of the endpoint.
func TestRevertSharedAIModelSplitsByMethods(t *testing.T) {
	first, err := convert.Convert([]byte(`
model_providers: [{name: openai, type: openai}]
models:
  - name: a
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {model: {values: [s]}}}
  - name: b
    capabilities: [generate]
    targets: [{name: gpt-5-mini, provider: openai}]
    config: {route: {methods: [POST], model: {values: [s]}}}
`), latestConvertOptions)
	require.NoError(t, err)
	deck1, err := first.Output.ToYAML()
	require.NoError(t, err)

	aigwYAML, warnings, err := Revert(deck1, Options{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	var doc aigw.Document
	require.NoError(t, yaml.Unmarshal(aigwYAML, &doc))
	require.Len(t, doc.Models, 2)

	second, err := convert.Convert(aigwYAML, latestConvertOptions)
	require.NoError(t, err)
	deck2, err := second.Output.ToYAML()
	require.NoError(t, err)
	require.Equal(t, string(deck1), string(deck2), "intermediate aigw:\n%s", aigwYAML)
}

// Models a and b share the alias s on one route. Revert cannot tell them
// apart, so it merges them and warns once.
func TestRevertWarnsSharedAIModelOnOneRoute(t *testing.T) {
	first, err := convert.Convert([]byte(`
model_providers: [{name: openai, type: openai}]
models:
  - name: a
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {model: {values: [s]}}}
  - name: b
    capabilities: [generate]
    targets: [{name: gpt-5-mini, provider: openai}]
    config: {route: {model: {values: [s]}}}
  - name: c
    capabilities: [generate]
    targets: [{name: gpt-5-nano, provider: openai}]
    config: {route: {model: {values: [s]}}}
`), latestConvertOptions)
	require.NoError(t, err)
	deck1, err := first.Output.ToYAML()
	require.NoError(t, err)

	_, warnings, err := Revert(deck1, Options{})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], `ai-models entry "s"`)

	_, _, err = Revert(deck1, Options{Strict: true})
	require.Error(t, err)
}
