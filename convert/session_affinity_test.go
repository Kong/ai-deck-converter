package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const sessionAffinityFixtures = overrideProvider + `
datastores:
  - name: shared-redis
    type: redis-ee
    config: {host: redis.internal, port: 6379}
  - name: ce-redis
    type: redis-ce
    config: {host: redis.internal}
`

func sessionAffinityModel(balancer string) string {
	return `models:
  - name: m
    capabilities: [generate]
    config:
      route: {paths: [/ai]}
      balancer: ` + balancer + `
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
` + sessionAffinityFixtures
}

func TestSessionAffinityBecomesConversationState(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		balancer string
		want     map[string]any
	}{
		"absent": {
			balancer: `{algorithm: round-robin}`,
			want:     nil,
		},
		"local": {
			balancer: `{algorithm: lowest-latency, latency_strategy: tpot, ` +
				`session_affinity: {enabled: true, ttl: 60, storage: {type: local}}}`,
			want: map[string]any{"enabled": true, "ttl": 60, "strategy": "local"},
		},
		"disabled without storage": {
			balancer: `{algorithm: priority, session_affinity: {enabled: false}}`,
			want:     map[string]any{"enabled": false, "strategy": "local"},
		},
		"redis gets the datastore connection": {
			balancer: `{algorithm: round-robin, session_affinity: ` +
				`{enabled: true, ttl: 21600, storage: {type: redis, datastore: {name: shared-redis}}}}`,
			want: map[string]any{
				"enabled":  true,
				"ttl":      21600,
				"strategy": "redis",
				"redis":    map[string]any{"host": "redis.internal", "port": 6379},
			},
		},
		"consistent-hashing has no affinity layer": {
			balancer: `{algorithm: consistent-hashing, session_affinity: {enabled: true}}`,
			want:     nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc, _, err := convertDoc([]byte(sessionAffinityModel(tc.balancer)), false)
			require.NoError(t, err)
			plugins := pluginsNamed(doc, "ai-proxy-advanced")
			require.NotEmpty(t, plugins)
			for _, p := range plugins {
				_, nested := p.Config["balancer"].(map[string]any)["session_affinity"]
				require.False(t, nested, "session_affinity must not stay under balancer")
				got, ok := p.Config["conversation_state"]
				if tc.want == nil {
					require.False(t, ok)
					continue
				}
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestSessionAffinityRejects(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		balancer string
		version  string
		want     string
	}{
		"unknown datastore": {
			balancer: `{algorithm: round-robin, session_affinity: {storage: {type: redis, datastore: {name: nope}}}}`,
			want:     `unknown datastore "nope"`,
		},
		"wrong datastore type": {
			balancer: `{algorithm: round-robin, session_affinity: {storage: {type: redis, datastore: {name: ce-redis}}}}`,
			want:     `has type "redis-ce", expected "redis-ee"`,
		},
		"runtime below 2.4": {
			balancer: `{algorithm: round-robin, session_affinity: {enabled: true}}`,
			version:  "2.3",
			want:     "session_affinity",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := testOptions
			if tc.version != "" {
				opts.MinRuntimeVersion = tc.version
			}
			result, err := Convert([]byte(sessionAffinityModel(tc.balancer)), opts)
			require.NoError(t, err)
			require.ErrorContains(t, issuesError(result.Issues, false), tc.want)
		})
	}
}

func TestSessionAffinityConflictOnSharedRoute(t *testing.T) {
	t.Parallel()

	src := `models:
  - name: a
    type: api
    capabilities: [generate]
    config:
      route: {paths: [/shared]}
      balancer: {algorithm: round-robin, session_affinity: {enabled: true}}
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
  - name: b
    type: api
    capabilities: [generate]
    config:
      route: {paths: [/shared]}
      balancer: {algorithm: round-robin, session_affinity: {enabled: false}}
    targets: [{name: t2, provider: openai-prod, config: {type: openai}}]
` + sessionAffinityFixtures

	_, _, err := convertDoc([]byte(src), false)
	require.ErrorContains(t, err, "different balancer.session_affinity")
}
