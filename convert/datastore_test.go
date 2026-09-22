package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Answerable from the policy type alone, with no conversion run.
func TestDatastorePathsForPolicyType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		policyType string
		want       []string
	}{
		{
			name:       "flat redis",
			policyType: "ai-rate-limiting-advanced",
			want:       []string{"redis"},
		},
		{
			name:       "acme nests under storage_config",
			policyType: "acme",
			want:       []string{"storage_config.redis"},
		},
		{
			name:       "datakit nests under resources.cache",
			policyType: "datakit",
			want:       []string{"resources.cache.redis"},
		},
		{
			// Both: the unselected sub-block is a ruled-out default, not
			// something the policy authored.
			name:       "vectordb reports both sub-blocks",
			policyType: "ai-rag-injector",
			want:       []string{"vectordb.redis", "vectordb.pgvector"},
		},
		{
			name:       "plugin takes no datastore",
			policyType: "request-transformer",
		},
		{
			name:       "no policy type",
			policyType: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, DatastorePathsForPolicyType(tc.policyType))
		})
	}
}

// Mirrors what ApplyDatastore accepts, so a caller checking ahead of a
// conversion and the conversion itself cannot disagree.
func TestDatastoreSupported(t *testing.T) {
	t.Parallel()

	require.True(t, DatastoreSupported("rate-limiting", "redis-ce"))
	require.True(t, DatastoreSupported("ai-rag-injector", "redis-ee"))
	require.True(t, DatastoreSupported("ai-rag-injector", "vectordb"))

	require.False(t, DatastoreSupported("rate-limiting", "redis-ee"))
	require.False(t, DatastoreSupported("ai-rag-injector", "redis-ce"))
	require.False(t, DatastoreSupported("acl", "redis-ce"))
	require.False(t, DatastoreSupported("", ""))
}
