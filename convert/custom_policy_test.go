package convert

import (
	"strconv"
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/stretchr/testify/require"
)

func TestConvertCustomPoliciesToDBLessPlugins(t *testing.T) {
	doc := &aigw.Document{CustomPolicies: []aigw.CustomPolicy{
		{
			ID:      "00000000-0000-0000-0000-000000000001",
			Name:    "streaming-policy",
			Type:    "streaming",
			Schema:  "return { name = 'streaming-policy' }",
			Handler: "return {}",
		},
		{
			Name:    "installed-policy",
			Type:    "installed",
			Schema:  "return { name = 'installed-policy' }",
			Handler: "return {}",
		},
	}}

	c := newConverter(doc, Options{})
	c.run()
	require.Empty(t, c.issues)
	require.Len(t, c.out.CustomPlugins, 1)
	require.Equal(t, "streaming-policy", c.out.CustomPlugins[0].Name)

	out := c.out.ToDBLess()
	require.Len(t, out.CustomPlugins, 1)
	plugin := out.CustomPlugins[0]
	require.Equal(t, "00000000-0000-0000-0000-000000000001", plugin.ID)
	require.Equal(t, "streaming-policy", plugin.Name)
	require.Equal(t, "return { name = 'streaming-policy' }", plugin.Schema)
	require.Equal(t, "return {}", plugin.Handler)
}

func TestCustomPolicyPriority(t *testing.T) {
	doc := &aigw.Document{CustomPolicies: []aigw.CustomPolicy{
		{Name: "early", Type: "streaming", Schema: "return {}", Handler: `return { PRIORITY = 1000, VERSION = "1.0.0" }`},
		{Name: "negative", Type: "streaming", Schema: "return {}", Handler: `return { PRIORITY = -5 }`},
		{Name: "no-priority", Type: "streaming", Schema: "return {}", Handler: `return { VERSION = "1.0.0" }`},
		{Name: "not-streaming", Type: "installed", Schema: "return {}", Handler: "return { PRIORITY = 1000 }"},
		{Name: "no-handler", Type: "streaming", Schema: "return {}"},
	}}
	c := newConverter(doc, Options{})

	for name, tc := range map[string]struct {
		want     int
		wantOK   bool
		priority int
	}{
		"early":         {1000, true, 0},
		"negative":      {-5, true, 0},
		"no-priority":   {0, false, 0},
		"not-streaming": {0, false, 0},
		"no-handler":    {0, false, 0},
		"unknown":       {0, false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			priority, ok := c.customPolicyPriority(name)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, tc.want, priority)
			}
		})
	}
}

func TestConvertWarnsCustomPolicyOutranksModelSelector(t *testing.T) {
	src := func(priority int) []byte {
		return []byte(`
model_providers:
  - name: p1
    type: openai
custom_policies:
  - name: streamer
    type: streaming
    schema: "return {}"
    handler: "return { PRIORITY = ` + strconv.Itoa(priority) + ` }"
policies:
  - name: streamer-instance
    type: streamer
    config: {}
models:
  - type: model
    name: guarded
    capabilities: [generate]
    formats: [{type: openai}]
    targets:
      - name: gpt-4o
        provider: p1
        config: {type: openai}
    policies: [streamer-instance]
    config:
      route: {paths: [/ai]}
`)
	}

	t.Run("outranks the selector", func(t *testing.T) {
		result, err := Convert(src(aimap.AIModelSelectorPriority+1), testOptions)
		require.NoError(t, err)
		issue := requireIssue(t, result.Issues, Warning, BehaviorMayDiffer, kindModel, "guarded", "policies[0]")
		require.Contains(t, issue.Reason, `custom policy "streamer" sets PRIORITY 958`)
		require.Contains(t, issue.Reason, "never runs")
	})

	t.Run("does not outrank the selector", func(t *testing.T) {
		result, err := Convert(src(aimap.AIModelSelectorPriority-1), testOptions)
		require.NoError(t, err)
		require.Empty(t, result.Issues)
	})
}

func TestConvertCustomPoliciesSkipsPoliciesWithoutStreamingHandler(t *testing.T) {
	for _, policy := range []aigw.CustomPolicy{
		{
			Name:   "streaming-policy",
			Type:   "streaming",
			Schema: "return { name = 'streaming-policy' }",
		},
		{
			Name:    "installed-policy",
			Type:    "installed",
			Schema:  "return { name = 'installed-policy' }",
			Handler: "return {}",
		},
	} {
		c := newConverter(&aigw.Document{CustomPolicies: []aigw.CustomPolicy{policy}}, Options{})

		c.run()
		require.Empty(t, c.issues)
		require.Empty(t, c.out.ToDBLess().CustomPlugins)
	}
}
