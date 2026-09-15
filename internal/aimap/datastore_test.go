package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyModelDatastorePgvector(t *testing.T) {
	t.Parallel()

	// Input in the shape VectorDBToPlugin emits: common keys at the top, the
	// strategy's settings under the sub-block named by strategy.
	vectordb := map[string]any{
		"strategy":        "pgvector",
		"dimensions":      1024,
		"distance_metric": "cosine",
		"threshold":       0.7,
		"pgvector":        map[string]any{"host": "placeholder.invalid"},
	}
	got, err := ApplyModelDatastore(vectordb, DatastoreTypeVectorDB, map[string]any{
		"host":       "pg.internal",
		"port":       5432,
		"ssl_verify": true, // already plugin-shaped: must not be re-lowered
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"strategy":        "pgvector",
		"dimensions":      1024,
		"distance_metric": "cosine",
		"threshold":       0.7,
		"pgvector":        map[string]any{"host": "pg.internal", "port": 5432, "ssl_verify": true},
	}, got, "common keys preserved, connection sub-block replaced with dsConfig verbatim")
	// The authored block must not be mutated in place.
	require.Equal(t, map[string]any{"host": "placeholder.invalid"}, vectordb["pgvector"])
}

func TestApplyModelDatastoreCreatesBlockFromNil(t *testing.T) {
	t.Parallel()

	got, err := ApplyModelDatastore(nil, DatastoreTypeRedisEE, map[string]any{"host": "redis.internal"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"strategy": "redis",
		"redis":    map[string]any{"host": "redis.internal"},
	}, got)
}

func TestApplyModelDatastoreRejectsRedisCE(t *testing.T) {
	t.Parallel()

	_, err := ApplyModelDatastore(nil, DatastoreTypeRedisCE, map[string]any{"host": "x"})
	require.Error(t, err, "no model accepts a redis-ce datastore")
	require.Contains(t, err.Error(), DatastoreTypeRedisCE)
	require.Contains(t, err.Error(), DatastoreTypeRedisEE)
}
