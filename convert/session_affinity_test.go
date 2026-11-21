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
		"consistent-hashing has no affinity layer": {
			balancer: `{algorithm: consistent-hashing, session_affinity: {enabled: true}}`,
			want:     "consistent-hashing balancer, which has no session affinity layer",
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

const datastorePurposeFixtures = overrideProvider + `
datastores:
  - name: vec-redis
    type: redis-ee
    config: {host: vec.internal, port: 6379}
  - name: shared-redis
    type: redis-ee
    config: {host: redis.internal, port: 6379}
  - name: ce-redis
    type: redis-ce
    config: {host: redis.internal}
`

func datastorePurposeModel(name, datastores, balancer string) string {
	return `models:
  - name: ` + name + `
    capabilities: [generate]
    datastores: ` + datastores + `
    config:
      route: {paths: [/` + name + `]}
      balancer: ` + balancer + `
    targets: [{name: t, provider: openai-prod, config: {type: openai}}]
` + datastorePurposeFixtures
}

func TestSessionAffinityDatastorePurpose(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		datastores string
		balancer   string
		want       map[string]any
	}{
		"session affinity datastore with round-robin": {
			datastores: `[{name: shared-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: round-robin, session_affinity: {enabled: true, storage: {type: redis}}}`,
			want: map[string]any{
				"enabled":  true,
				"strategy": "redis",
				"redis":    map[string]any{"host": "redis.internal", "port": 6379},
			},
		},
		"underscore spelling is accepted": {
			datastores: `[{name: shared-redis, purpose: session_affinity}]`,
			balancer:   `{algorithm: round-robin, session_affinity: {storage: {type: redis}}}`,
			want: map[string]any{
				"strategy": "redis",
				"redis":    map[string]any{"host": "redis.internal", "port": 6379},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc, _, err := convertDoc([]byte(datastorePurposeModel("m", tc.datastores, tc.balancer)), false)
			require.NoError(t, err)
			plugins := pluginsNamed(doc, "ai-proxy-advanced")
			require.Len(t, plugins, 1)
			require.Equal(t, tc.want, plugins[0].Config["conversation_state"])
			_, hasVDB := plugins[0].Config["vectordb"]
			require.False(t, hasVDB)
		})
	}
}

func TestSessionAffinityDatastorePurposeSemantic(t *testing.T) {
	t.Parallel()

	balancer := `{algorithm: semantic, ` +
		`embeddings: {name: text-embedding-3-small, provider: openai-prod, config: {type: openai}}, ` +
		`vectordb: {strategy: redis, dimensions: 3, distance_metric: cosine}, ` +
		`session_affinity: {enabled: true, storage: {type: redis}}}`
	datastores := `[{name: vec-redis, purpose: semantic-balancer}, {name: shared-redis, purpose: session-affinity}]`

	doc, _, err := convertDoc([]byte(datastorePurposeModel("m", datastores, balancer)), false)
	require.NoError(t, err)
	plugins := pluginsNamed(doc, "ai-proxy-advanced")
	require.Len(t, plugins, 1)

	require.Equal(t, map[string]any{
		"strategy":        "redis",
		"dimensions":      3,
		"distance_metric": "cosine",
		"redis":           map[string]any{"host": "vec.internal", "port": 6379},
	}, plugins[0].Config["vectordb"])
	require.Equal(t, map[string]any{
		"enabled":  true,
		"strategy": "redis",
		"redis":    map[string]any{"host": "redis.internal", "port": 6379},
	}, plugins[0].Config["conversation_state"])
}

func TestSessionAffinityDatastorePurposeRejects(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		datastores string
		balancer   string
		want       string
	}{
		"semantic-balancer purpose on a non semantic balancer": {
			datastores: `[{name: vec-redis, purpose: semantic-balancer}]`,
			balancer:   `{algorithm: round-robin}`,
			want:       "requires the semantic balancer",
		},
		"unknown purpose": {
			datastores: `[{name: shared-redis, purpose: teleport}]`,
			balancer:   `{algorithm: round-robin}`,
			want:       `unknown datastore purpose "teleport"`,
		},
		"two embeddings entries": {
			datastores: `[{name: vec-redis, purpose: semantic-balancer}, {name: shared-redis, purpose: semantic-balancer}]`,
			balancer:   `{algorithm: semantic}`,
			want:       `more than one datastore with purpose "semantic-balancer"`,
		},
		"two session affinity entries": {
			datastores: `[{name: vec-redis, purpose: session-affinity}, {name: shared-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: round-robin, session_affinity: {storage: {type: redis}}}`,
			want:       `more than one datastore with purpose "session-affinity"`,
		},
		"session affinity datastore with local storage": {
			datastores: `[{name: shared-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: round-robin, session_affinity: {storage: {type: local}}}`,
			want:       "session affinity storage is not redis",
		},
		"session affinity datastore without a block": {
			datastores: `[{name: shared-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: round-robin}`,
			want:       "has no session affinity block",
		},
		"session affinity datastore with consistent hashing": {
			datastores: `[{name: shared-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: consistent-hashing}`,
			want:       "consistent-hashing balancer, which has no session affinity layer",
		},
		"datastore named in both places": {
			datastores: `[{name: shared-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: round-robin, session_affinity: {storage: {type: redis, datastore: {name: vec-redis}}}}`,
			want:       `names datastore "vec-redis"`,
		},
		"session affinity datastore of the wrong type": {
			datastores: `[{name: ce-redis, purpose: session-affinity}]`,
			balancer:   `{algorithm: round-robin, session_affinity: {storage: {type: redis}}}`,
			want:       `expected "redis-ee"`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result, err := Convert([]byte(datastorePurposeModel("m", tc.datastores, tc.balancer)), testOptions)
			require.NoError(t, err)
			require.ErrorContains(t, issuesError(result.Issues, false), tc.want)
		})
	}
}

func TestSessionAffinityInlineRedis(t *testing.T) {
	t.Parallel()

	t.Run("inline connection is used without a datastore", func(t *testing.T) {
		t.Parallel()

		balancer := `{algorithm: round-robin, session_affinity: {` +
			`storage: {type: redis, host: inline.internal, port: 6390}}}`
		doc, _, err := convertDoc([]byte(datastorePurposeModel("inline", `[]`, balancer)), false)
		require.NoError(t, err)
		plugins := pluginsNamed(doc, "ai-proxy-advanced")
		require.Len(t, plugins, 1)
		require.Equal(t, map[string]any{
			"strategy": "redis",
			"redis":    map[string]any{"host": "inline.internal", "port": 6390},
		}, plugins[0].Config["conversation_state"])
	})

	t.Run("datastore wins over the inline connection", func(t *testing.T) {
		t.Parallel()

		balancer := `{algorithm: round-robin, session_affinity: {` +
			`storage: {type: redis, host: inline.internal, port: 6390}}}`
		datastores := `[{name: shared-redis, purpose: session-affinity}]`
		doc, _, err := convertDoc([]byte(datastorePurposeModel("both", datastores, balancer)), false)
		require.NoError(t, err)
		plugins := pluginsNamed(doc, "ai-proxy-advanced")
		require.Len(t, plugins, 1)
		require.Equal(t, map[string]any{
			"strategy": "redis",
			"redis":    map[string]any{"host": "redis.internal", "port": 6379},
		}, plugins[0].Config["conversation_state"])
	})
}

func TestSessionAffinityRedisNeedsAConnection(t *testing.T) {
	t.Parallel()

	balancer := `{algorithm: round-robin, session_affinity: {storage: {type: redis}}}`
	result, err := Convert([]byte(datastorePurposeModel("none", `[]`, balancer)), testOptions)
	require.NoError(t, err)
	require.ErrorContains(t, issuesError(result.Issues, false),
		"needs an inline redis connection or a datastore")
}
