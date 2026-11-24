package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func intelligentModel(balancer string, formats string) string {
	return `models:
  - name: m
    capabilities: [generate]
    formats: ` + formats + `
    config:
      route: {paths: [/ai]}
      balancer: ` + balancer + `
    targets: [{name: gpt-4o-mini, provider: openai-prod, config: {type: openai}}]
` + overrideProvider
}

const intelligentClassifier = `classifier: {type: custom, url: "https://c.internal", ` +
	`format: /v1/systemone, name: jev-latest, context_window: 4096}`

func TestIntelligentBalancerConfig(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		balancer     string
		wantTradeoff float64
	}{
		"default tradeoff is balanced": {
			balancer:     `{algorithm: intelligent, ` + intelligentClassifier + `}`,
			wantTradeoff: 0.5,
		},
		"cost": {
			balancer:     `{algorithm: intelligent, cost_quality_tradeoff: cost, ` + intelligentClassifier + `}`,
			wantTradeoff: 0,
		},
		"number passes through": {
			balancer:     `{algorithm: intelligent, cost_quality_tradeoff: 0.3, ` + intelligentClassifier + `}`,
			wantTradeoff: 0.3,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc, warnings, err := convertDoc([]byte(intelligentModel(tc.balancer, `[{type: openai}]`)), false)
			require.NoError(t, err)
			require.Empty(t, warnings)
			plugins := pluginsNamed(doc, "ai-proxy-advanced")
			require.Len(t, plugins, 1)
			balancer := plugins[0].Config["balancer"].(map[string]any)
			require.Equal(t, "intelligent", balancer["algorithm"])
			require.InDelta(t, tc.wantTradeoff, balancer["cost_quality_tradeoff"], 0)
			require.NotContains(t, balancer, "version")
			require.NotContains(t, balancer, "strategy")
		})
	}
}

func TestIntelligentBalancerIssues(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		balancer    string
		formats     string
		wantErr     string
		wantWarning string
	}{
		"non-default strategy warns": {
			balancer:    `{algorithm: intelligent, strategy: margin, ` + intelligentClassifier + `}`,
			wantWarning: `the data plane has no intelligent balancer strategy and uses "weighted", not margin`,
		},
		"default strategy and version are dropped silently": {
			balancer: `{algorithm: intelligent, strategy: weighted, version: v1, ` + intelligentClassifier + `}`,
		},
		"unknown version": {
			balancer: `{algorithm: intelligent, version: v2, ` + intelligentClassifier + `}`,
			wantErr:  "intelligent balancer version v2 is unknown",
		},
		"unknown tradeoff": {
			balancer: `{algorithm: intelligent, cost_quality_tradeoff: fast, ` + intelligentClassifier + `}`,
			wantErr:  "cost_quality_tradeoff fast is not cost, balanced, quality or a number from 0 to 1",
		},
		"missing classifier": {
			balancer: `{algorithm: intelligent}`,
			wantErr:  "the intelligent balancer needs a classifier",
		},
		"classifier auth params and extra headers warn": {
			balancer: `{algorithm: intelligent, classifier: {type: custom, url: "https://c.internal", ` +
				`auth: {type: basic, headers: [{name: A, value: a}, {name: B, value: b}], ` +
				`params: [{name: key, value: k}]}}}`,
			wantWarning: "the classifier auth keeps only its first header and drops params, headers[1]",
		},
		"passthrough format": {
			balancer: `{algorithm: intelligent, ` + intelligentClassifier + `}`,
			formats:  `[{type: passthrough}]`,
			wantErr:  "the passthrough format cannot use the intelligent balancer algorithm",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			formats := tc.formats
			if formats == "" {
				formats = `[{type: openai}]`
			}
			_, warnings, err := convertDoc([]byte(intelligentModel(tc.balancer, formats)), false)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.wantWarning == "" {
				require.Empty(t, warnings)
				return
			}
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], tc.wantWarning)
		})
	}
}
