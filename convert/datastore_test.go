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

// Answerable from the policy type alone, with no conversion run.
func TestDatastoreStrategyPathForPolicyType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		policyType string
		want       string
	}{
		{
			name:       "vectordb family nests inside its block",
			policyType: "ai-semantic-cache",
			want:       "vectordb.strategy",
		},
		{
			name:       "flat plugins switch at strategy",
			policyType: "rate-limiting-advanced",
			want:       "strategy",
		},
		{
			name:       "rate-limiting calls it policy",
			policyType: "rate-limiting",
			want:       "policy",
		},
		{
			name:       "acme calls it storage",
			policyType: "acme",
			want:       "storage",
		},
		{
			name:       "datakit switches inside its cache block",
			policyType: "datakit",
			want:       "resources.cache.strategy",
		},
		{
			name:       "plugin takes no datastore",
			policyType: "request-transformer",
			want:       "",
		},
		{
			name:       "no policy type",
			policyType: "",
			want:       "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, DatastoreStrategyPathForPolicyType(tc.policyType))
		})
	}
}

// Mirrors the backends the conversion substitutes for, so a caller checking
// ahead of a conversion and the conversion itself cannot disagree.
func TestDatastoreStrategyForDatastoreType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		datastoreType string
		wantStrategy  string
		wantOk        bool
	}{
		{
			name:          "redis-ce speaks redis",
			datastoreType: "redis-ce",
			wantStrategy:  "redis",
			wantOk:        true,
		},
		{
			name:          "redis-ee speaks redis",
			datastoreType: "redis-ee",
			wantStrategy:  "redis",
			wantOk:        true,
		},
		{
			name:          "vectordb speaks pgvector",
			datastoreType: "vectordb",
			wantStrategy:  "pgvector",
			wantOk:        true,
		},
		{
			name:          "unknown type",
			datastoreType: "postgres",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			strategy, ok := DatastoreStrategyForDatastoreType(tc.datastoreType)
			require.Equal(t, tc.wantStrategy, strategy)
			require.Equal(t, tc.wantOk, ok)
		})
	}
}
