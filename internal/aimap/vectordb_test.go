package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVectorDBToPluginPgvector(t *testing.T) {
	t.Parallel()

	got := VectorDBToPlugin(map[string]any{
		"strategy":        "pgvector",
		"distance_metric": "cosine",
		"threshold":       0.7,
		"dimensions":      1024,
		"database":        "kong-pgvector",
		"host":            "127.0.0.1",
		"port":            5432,
		"user":            "my-user",
		"password":        "my-password",
		"timeout":         5000,
		"ssl": map[string]any{
			"enabled":  true,
			"cert":     "/path/to/cert.pem",
			"cert_key": "/path/to/cert.key",
			"required": true,
			"verify":   true,
			"version":  "any",
		},
	})

	require.Equal(t, map[string]any{
		"strategy":        "pgvector",
		"distance_metric": "cosine",
		"threshold":       0.7,
		"dimensions":      1024,
		"pgvector": map[string]any{
			"database":     "kong-pgvector",
			"host":         "127.0.0.1",
			"port":         5432,
			"user":         "my-user",
			"password":     "my-password",
			"timeout":      5000,
			"ssl":          true,
			"ssl_cert":     "/path/to/cert.pem",
			"ssl_cert_key": "/path/to/cert.key",
			"ssl_required": true,
			"ssl_verify":   true,
			"ssl_version":  "any",
		},
	}, got)
}

// Redis groups its cluster, connection-pool, and sentinel settings; the ssl
// keys it carries flat (unlike pgvector) must stay flat.
func TestVectorDBToPluginRedis(t *testing.T) {
	t.Parallel()

	got := VectorDBToPlugin(map[string]any{
		"strategy":   "redis",
		"dimensions": 1024,
		"host":       "127.0.0.1",
		"port":       6379,
		"password":   "my-password",
		"username":   "my-username",
		"ssl":        true,
		"ssl_verify": true,
		"cluster": map[string]any{
			"max_redirections": 5,
			"nodes":            []any{map[string]any{"ip": "127.0.0.1", "port": 6379}},
		},
		"keepalive": map[string]any{"backlog": 10000, "pool_size": 256},
		"sentinel":  map[string]any{"master": "mymaster", "role": "master"},
		// A record the plugin nests too, so it is not a group.
		"cloud_authentication": map[string]any{"auth_provider": "aws"},
	})

	require.Equal(t, map[string]any{
		"strategy":   "redis",
		"dimensions": 1024,
		"redis": map[string]any{
			"host":                     "127.0.0.1",
			"port":                     6379,
			"password":                 "my-password",
			"username":                 "my-username",
			"ssl":                      true,
			"ssl_verify":               true,
			"cluster_max_redirections": 5,
			"cluster_nodes":            []any{map[string]any{"ip": "127.0.0.1", "port": 6379}},
			"keepalive_backlog":        10000,
			"keepalive_pool_size":      256,
			"sentinel_master":          "mymaster",
			"sentinel_role":            "master",
			"cloud_authentication":     map[string]any{"auth_provider": "aws"},
		},
	}, got)
}

// An already-nested sub-block is the plugin shape, so lowering it again (as
// happens when a reverted config is re-converted) must not move it a level
// deeper.
func TestVectorDBToPluginIdempotent(t *testing.T) {
	t.Parallel()

	for _, lowered := range []map[string]any{
		{
			"strategy":   "pgvector",
			"dimensions": 1024,
			"pgvector":   map[string]any{"host": "127.0.0.1", "ssl": true},
		},
		{
			"strategy":   "redis",
			"dimensions": 1024,
			"redis":      map[string]any{"host": "127.0.0.1", "cluster_max_redirections": 5},
		},
	} {
		require.Equal(t, lowered, VectorDBToPlugin(lowered))
	}
}

// Flat and nested authoring of the same strategy merge into one sub-block.
func TestVectorDBToPluginMergesNestedAndFlat(t *testing.T) {
	t.Parallel()

	got := VectorDBToPlugin(map[string]any{
		"strategy": "pgvector",
		"host":     "127.0.0.1",
		"pgvector": map[string]any{"port": 5432},
	})
	require.Equal(t, map[string]any{
		"strategy": "pgvector",
		"pgvector": map[string]any{"host": "127.0.0.1", "port": 5432},
	}, got)
}

// Without a recognized strategy there is no sub-block to nest under, so the
// block is passed through as authored rather than guessed at.
func TestVectorDBToPluginUnknownStrategy(t *testing.T) {
	t.Parallel()

	block := map[string]any{"strategy": "elastic", "host": "es.local"}
	require.Equal(t, block, VectorDBToPlugin(block))
	require.Equal(t, "not-a-map", VectorDBToPlugin("not-a-map"))
	require.Nil(t, VectorDBToPlugin(nil))
}

// Lifting a lowered block recovers the authored AI Gateway shape, for both
// strategies and both group spellings.
func TestVectorDBFromPluginRoundTrip(t *testing.T) {
	t.Parallel()

	for _, source := range []map[string]any{
		{
			"strategy":        "pgvector",
			"distance_metric": "cosine",
			"threshold":       0.7,
			"dimensions":      1024,
			"database":        "kong-pgvector",
			"host":            "127.0.0.1",
			"port":            5432,
			"user":            "my-user",
			"password":        "my-password",
			"timeout":         5000,
			"ssl": map[string]any{
				"enabled":  true,
				"cert":     "/path/to/cert.pem",
				"cert_key": "/path/to/cert.key",
				"required": true,
				"verify":   true,
				"version":  "any",
			},
		},
		{
			"strategy":   "redis",
			"dimensions": 1024,
			"host":       "127.0.0.1",
			"port":       6379,
			"password":   "my-password",
			"ssl":        true,
			"ssl_verify": true,
			"cluster": map[string]any{
				"max_redirections": 5,
				"nodes":            []any{map[string]any{"ip": "127.0.0.1", "port": 6379}},
			},
			"keepalive":            map[string]any{"backlog": 10000, "pool_size": 256},
			"cloud_authentication": map[string]any{"auth_provider": "aws"},
		},
	} {
		require.Equal(t, source, VectorDBFromPlugin(VectorDBToPlugin(source)))
	}
}

// A block with no sub-block for its strategy is already in the AI Gateway shape
// (or was hand-written that way), so lifting leaves it alone.
func TestVectorDBFromPluginPassthrough(t *testing.T) {
	t.Parallel()

	block := map[string]any{"strategy": "pgvector", "host": "127.0.0.1"}
	require.Equal(t, block, VectorDBFromPlugin(block))
	require.Nil(t, VectorDBFromPlugin(nil))
}

// Group fields outside the schema survive both directions under the group's
// own prefix rather than being dropped.
func TestVectorDBUnknownGroupField(t *testing.T) {
	t.Parallel()

	source := map[string]any{
		"strategy": "pgvector",
		"ssl":      map[string]any{"enabled": true, "sni": "db.local"},
	}
	lowered := VectorDBToPlugin(source)
	require.Equal(t, map[string]any{
		"strategy": "pgvector",
		"pgvector": map[string]any{"ssl": true, "ssl_sni": "db.local"},
	}, lowered)
	require.Equal(t, source, VectorDBFromPlugin(lowered))
}
