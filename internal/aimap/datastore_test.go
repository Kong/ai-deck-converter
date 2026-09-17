package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyTypeSupportsDatastore(t *testing.T) {
	t.Parallel()

	require.True(t, PolicyTypeSupportsDatastore("rate-limiting"))
	require.True(t, PolicyTypeSupportsDatastore("ai-rag-injector"))
	require.False(t, PolicyTypeSupportsDatastore("request-transformer"))
	require.False(t, PolicyTypeSupportsDatastore(""))
}

func TestDatastoreSupportForPolicyTypeUnknownPlugin(t *testing.T) {
	t.Parallel()

	support, allowed := DatastoreSupportForPolicyType("request-transformer", DatastoreTypeRedisCE)
	require.False(t, allowed)
	require.Equal(t, DatastoreSupport{}, support)
}

func TestDatastoreSupportForPolicyTypeWrongDatastoreType(t *testing.T) {
	t.Parallel()

	support, allowed := DatastoreSupportForPolicyType("rate-limiting", DatastoreTypeRedisEE)
	require.False(t, allowed)
	require.True(t, PolicyTypeSupportsDatastore("rate-limiting"), "it consumes a datastore, just not this type")
	require.Equal(t, "redis", support.ConfigPath)
}

func TestDatastoreSupportForPolicyTypeAllowed(t *testing.T) {
	t.Parallel()

	support, allowed := DatastoreSupportForPolicyType("ai-rag-injector", DatastoreTypeVectorDB)
	require.True(t, allowed)
	require.Equal(t, "vectordb", support.ConfigPath)
}

func TestApplyDatastoreRejectsWrongDatastoreType(t *testing.T) {
	t.Parallel()

	_, err := ApplyDatastore(
		map[string]any{}, "rate-limiting", DatastoreTypeRedisEE,
		map[string]any{"host": "redis.internal"},
	)
	require.EqualError(t, err, `plugin type "rate-limiting" does not support a "redis-ee" datastore`)
}

func TestApplyDatastoreSubstitutesConnection(t *testing.T) {
	t.Parallel()

	got, err := ApplyDatastore(
		map[string]any{"minute": 100}, "rate-limiting", DatastoreTypeRedisCE,
		map[string]any{"host": "redis.internal"},
	)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"minute": 100,
		"redis":  map[string]any{"host": "redis.internal"},
	}, got)
}
