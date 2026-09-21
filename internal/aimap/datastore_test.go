package aimap

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
)

func TestPolicyTypeSupportsDatastore(t *testing.T) {
	t.Parallel()

	require.True(t, PolicyTypeSupportsDatastore("rate-limiting"))
	require.True(t, PolicyTypeSupportsDatastore("ai-rag-injector"))
	require.False(t, PolicyTypeSupportsDatastore("request-transformer"))
	require.False(t, PolicyTypeSupportsDatastore(""))
}

func TestDatastoreSupportForPolicyType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		policyType    string
		datastoreType string
		allowed       bool
		configPath    string
	}{
		{
			name:          "unrecognized plugin",
			policyType:    "request-transformer",
			datastoreType: DatastoreTypeRedisCE,
		},
		{
			name:          "recognized plugin, wrong type",
			policyType:    "rate-limiting",
			datastoreType: DatastoreTypeRedisEE,
			configPath:    "redis",
		},
		{
			name:          "vectordb plugin takes pgvector",
			policyType:    "ai-rag-injector",
			datastoreType: DatastoreTypeVectorDB,
			allowed:       true,
			configPath:    "vectordb",
		},
		{
			name:          "vectordb plugin takes redis-ee",
			policyType:    "ai-semantic-cache",
			datastoreType: DatastoreTypeRedisEE,
			allowed:       true,
			configPath:    "vectordb",
		},
		{
			name:          "vectordb plugin never takes redis-ce",
			policyType:    "ai-rag-injector",
			datastoreType: DatastoreTypeRedisCE,
			configPath:    "vectordb",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			support, allowed := DatastoreSupportForPolicyType(tc.policyType, tc.datastoreType)
			require.Equal(t, tc.allowed, allowed)
			require.Equal(t, tc.configPath, support.ConfigPath)
		})
	}
}

// registry is the name-indexed Datastore set ApplyDatastore resolves against.
func registry(entries map[string]string) map[string]*aigw.Datastore {
	out := make(map[string]*aigw.Datastore, len(entries))
	for name, datastoreType := range entries {
		out[name] = &aigw.Datastore{
			Name:   name,
			Type:   datastoreType,
			Config: map[string]any{"host": name + ".internal"},
		}
	}
	return out
}

// refs builds a policy's datastore reference list from the names it cites.
func refs(names ...string) []aigw.DatastoreRef {
	out := make([]aigw.DatastoreRef, len(names))
	for i, name := range names {
		out[i] = aigw.DatastoreRef{Name: name}
	}
	return out
}

func TestApplyDatastoreRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		policyType string
		refs       []aigw.DatastoreRef
		registry   map[string]*aigw.Datastore
		wantErr    string
	}{
		{
			name:       "plugin type consumes no datastore",
			policyType: "request-transformer",
			refs:       refs("ds1"),
			registry:   registry(map[string]string{"ds1": DatastoreTypeRedisCE}),
			wantErr:    `plugin type "request-transformer" does not support datastores`,
		},
		{
			name:       "more than one datastore",
			policyType: "rate-limiting",
			refs:       refs("ds1", "ds2"),
			registry: registry(map[string]string{
				"ds1": DatastoreTypeRedisCE,
				"ds2": DatastoreTypeRedisCE,
			}),
			wantErr: "a policy may reference at most one datastore",
		},
		{
			// Both rules are violated; the plugin type wins, so trimming the
			// list is not offered as a fix that would not work.
			name:       "no support outranks cardinality",
			policyType: "request-transformer",
			refs:       refs("ds1", "ds2"),
			registry: registry(map[string]string{
				"ds1": DatastoreTypeRedisCE,
				"ds2": DatastoreTypeRedisCE,
			}),
			wantErr: `plugin type "request-transformer" does not support datastores`,
		},
		{
			name:       "wrong datastore type",
			policyType: "rate-limiting",
			refs:       refs("ds1"),
			registry:   registry(map[string]string{"ds1": DatastoreTypeRedisEE}),
			wantErr:    `plugin type "rate-limiting" does not support a "redis-ee" datastore`,
		},
		{
			name:       "vectordb plugin rejects redis-ce",
			policyType: "ai-rag-injector",
			refs:       refs("ds1"),
			registry:   registry(map[string]string{"ds1": DatastoreTypeRedisCE}),
			wantErr:    `plugin type "ai-rag-injector" does not support a "redis-ce" datastore`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ApplyDatastore(map[string]any{"minute": 100}, tc.policyType, tc.refs, tc.registry)
			require.EqualError(t, err, tc.wantErr)
			require.Nil(t, got)
		})
	}
}

func TestApplyDatastoreWithNoReferencesLeavesConfigUntouched(t *testing.T) {
	t.Parallel()

	config := map[string]any{"minute": 100}
	got, err := ApplyDatastore(config, "request-transformer", nil, nil)
	require.NoError(t, err)
	require.Equal(t, config, got)
}

func TestApplyDatastoreReportsUnknownReferenceWithConfigIntact(t *testing.T) {
	t.Parallel()

	config := map[string]any{"minute": 100}
	got, err := ApplyDatastore(config, "rate-limiting", refs("missing"), registry(nil))

	var unknown *UnknownDatastoreError
	require.ErrorAs(t, err, &unknown, "a dangling reference must be distinguishable, not a bare error")
	require.Equal(t, "missing", unknown.Name)
	require.EqualError(t, err, `references unknown datastore "missing"`)
	require.Equal(t, config, got, "config is returned intact so a tolerant caller can carry on")
}

func TestApplyDatastoreSubstitutesAtConfigPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		policyType    string
		datastoreType string
		want          map[string]any
	}{
		{
			name:          "flat redis",
			policyType:    "rate-limiting",
			datastoreType: DatastoreTypeRedisCE,
			want: map[string]any{
				"redis": map[string]any{"host": "ds1.internal"},
			},
		},
		{
			name:          "acme nests under storage_config",
			policyType:    "acme",
			datastoreType: DatastoreTypeRedisCE,
			want: map[string]any{
				"storage_config": map[string]any{
					"redis": map[string]any{"host": "ds1.internal"},
				},
			},
		},
		{
			name:          "datakit nests under resources.cache",
			policyType:    "datakit",
			datastoreType: DatastoreTypeRedisEE,
			want: map[string]any{
				"resources": map[string]any{
					"cache": map[string]any{
						"redis": map[string]any{"host": "ds1.internal"},
					},
				},
			},
		},
		{
			name:          "vectordb picks the pgvector sub-block",
			policyType:    "ai-rag-injector",
			datastoreType: DatastoreTypeVectorDB,
			want: map[string]any{
				"vectordb": map[string]any{
					"strategy": "pgvector",
					"pgvector": map[string]any{"host": "ds1.internal"},
				},
			},
		},
		{
			name:          "vectordb picks the redis sub-block",
			policyType:    "ai-semantic-cache",
			datastoreType: DatastoreTypeRedisEE,
			want: map[string]any{
				"vectordb": map[string]any{
					"strategy": "redis",
					"redis":    map[string]any{"host": "ds1.internal"},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ApplyDatastore(
				map[string]any{"minute": 100}, tc.policyType, refs("ds1"),
				registry(map[string]string{"ds1": tc.datastoreType}),
			)
			require.NoError(t, err)

			want := map[string]any{"minute": 100}
			for key, value := range tc.want {
				want[key] = value
			}
			require.Equal(t, want, got)
		})
	}
}

func TestApplyModelDatastoreRejectsUnsupportedTypeListsAllSupportedTypes(t *testing.T) {
	t.Parallel()

	_, err := ApplyModelDatastore(nil, DatastoreTypeRedisCE, map[string]any{"host": "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), DatastoreTypeRedisCE, "should name the rejected type")
	// Assert against modelDatastoreTypes itself, not a hardcoded list of type
	// names — this is exactly what the error message's extensibility change is
	// for: adding a third supported type shouldn't require touching this test,
	// since both the message and this assertion derive from the same map.
	for supported := range modelDatastoreTypes {
		require.Contains(t, err.Error(), supported, "message should list every currently-supported type")
	}
}

func TestApplyModelDatastoreRejectsUnknownTypeListsAllSupportedTypes(t *testing.T) {
	t.Parallel()

	_, err := ApplyModelDatastore(nil, "not-a-real-type", map[string]any{"host": "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not-a-real-type")
	for supported := range modelDatastoreTypes {
		require.Contains(t, err.Error(), supported)
	}
}
