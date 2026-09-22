package aimap

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
)

// Datastore type discriminators (aigw.Datastore.Type values). Named here,
// where they're actually validated and interpreted, so nothing compares
// against a bare string literal.
const (
	DatastoreTypeRedisCE  = "redis-ce"
	DatastoreTypeRedisEE  = "redis-ee"
	DatastoreTypeVectorDB = "vectordb" // despite the name, always pgvector; see datastoreTypeVectorDBMap.
)

const (
	VectorDBConfigPath       = "vectordb"
	VectorDBStrategyRedis    = "redis"
	VectorDBStrategyPGVector = "pgvector"
)

// datastoreTypeVectorDBMap maps a Datastore's `type` discriminator to the
// vectordb-consuming plugins' sub-block name that carries connection config
// for that engine. redis-ce and redis-ee are tracked as distinct Datastore
// types (their connection schemas differ: redis-ee adds cluster/sentinel/
// keepalive/timeout fields redis-ce doesn't have) but both nest under the
// plugins' single "redis" strategy sub-block — every vectordb-consuming AI
// Gateway plugin (ai-rag-injector, ai-semantic-cache, ai-semantic-prompt-guard,
// ai-semantic-response-guard; confirmed against each plugin's reference docs)
// exposes one unified redis field surface that a redis-ce Datastore just
// populates a subset of. A vectordb-typed Datastore (a pgvector connection,
// despite the name) speaks the "pgvector" sub-block. Shared by convert and
// revert so the two directions can't drift.
var datastoreTypeVectorDBMap = map[string]string{
	DatastoreTypeRedisCE:  VectorDBStrategyRedis,
	DatastoreTypeRedisEE:  VectorDBStrategyRedis,
	DatastoreTypeVectorDB: VectorDBStrategyPGVector,
}

// VectorDBStrategyForDatastoreType returns the vectordb plugin sub-block name
// (its strategy value) a Datastore of the given type supplies connection
// config for, and whether the type is recognized.
func VectorDBStrategyForDatastoreType(datastoreType string) (string, bool) {
	group, ok := datastoreTypeVectorDBMap[datastoreType]
	return group, ok
}

// DatastoreSupport describes how a policy plugin type consumes a Datastore.
// AllowedTypes lists every Datastore type this plugin accepts — one for most
// plugins, but two for the VectorDB family (redis-ee or vectordb; never
// redis-ce). ConfigPath is the dot-path under the plugin's config where the
// connection gets substituted — straight from that plugin's own
// `supported_partials` schema declaration, the source of truth this table
// encodes.
//
// VectorDB plugins nest the Datastore under config.<ConfigPath>.<redis|pgvector>,
// the sub-block chosen dynamically by vectordb.strategy. Every other
// supported plugin type assigns it directly at config.<ConfigPath> — no
// strategy, no sub-block choice.
type DatastoreSupport struct {
	AllowedTypes map[string]struct{}
	ConfigPath   string
}

// Allows reports whether datastoreType is one this plugin type accepts.
func (s DatastoreSupport) Allows(datastoreType string) bool {
	_, ok := s.AllowedTypes[datastoreType]
	return ok
}

// datastoreSupportedPolicyTypes is every Kong plugin usable as an AI Gateway
// policy (per https://developer.konghq.com/ai-gateway/policies/) whose own
// schema declares support for a redis or vectordb partial.
var datastoreSupportedPolicyTypes = map[string]DatastoreSupport{
	"ai-rag-injector": {
		ConfigPath:   "vectordb",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}, DatastoreTypeVectorDB: {}},
	},
	"ai-semantic-cache": {
		ConfigPath:   "vectordb",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}, DatastoreTypeVectorDB: {}},
	},
	"ai-semantic-prompt-guard": {
		ConfigPath:   "vectordb",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}, DatastoreTypeVectorDB: {}},
	},
	"ai-semantic-response-guard": {
		ConfigPath:   "vectordb",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}, DatastoreTypeVectorDB: {}},
	},
	"ai-rate-limiting-advanced": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
	"acme": {
		ConfigPath:   "storage_config.redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisCE: {}},
	},
	"rate-limiting": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisCE: {}},
	},
	"response-ratelimiting": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisCE: {}},
	},
	"datakit": {
		ConfigPath:   "resources.cache.redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
	"graphql-proxy-cache-advanced": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
	"graphql-rate-limiting-advanced": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
	"proxy-cache-advanced": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
	"rate-limiting-advanced": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
	"service-protection": {
		ConfigPath:   "redis",
		AllowedTypes: map[string]struct{}{DatastoreTypeRedisEE: {}},
	},
}

// PolicyTypeSupportsDatastore reports whether policyType consumes a Datastore
// at all, for callers with no concrete Datastore type yet to check.
func PolicyTypeSupportsDatastore(policyType string) bool {
	_, known := datastoreSupportedPolicyTypes[policyType]
	return known
}

// DatastoreSupportForPolicyType returns how the given policy plugin type
// consumes a Datastore, and whether datastoreType is one it accepts. An
// unrecognized policyType returns a zero DatastoreSupport (ConfigPath == "")
// and allowed == false; callers that need to tell that apart from a recognized
// plugin rejecting this one type ask PolicyTypeSupportsDatastore.
func DatastoreSupportForPolicyType(policyType, datastoreType string) (support DatastoreSupport, allowed bool) {
	support, ok := datastoreSupportedPolicyTypes[policyType]
	if !ok {
		return DatastoreSupport{}, false
	}
	return support, support.Allows(datastoreType)
}

// UnknownDatastoreError reports a reference to a name absent from the registry.
// ApplyDatastore returns it alongside the unchanged config rather than a bare
// error, so a caller that tolerates dangling references — converting a
// hand-written config without -strict — can warn and carry on. Whether a
// dangling reference is fatal is the caller's decision, not this package's.
type UnknownDatastoreError struct {
	Name string
}

func (e *UnknownDatastoreError) Error() string {
	return fmt.Sprintf("references unknown datastore %q", e.Name)
}

// ApplyDatastore validates a policy's Datastore references and substitutes the
// named connection into the plugin's config, at the dot-path policyType
// declares in datastoreSupportedPolicyTypes. refs is the policy's reference
// list as the author wrote it, resolved against registry; referencing none is
// not an error.
//
// Support is checked before the at-most-one ceiling: a plugin taking no
// Datastore would still fail after the author trims the list to one. A name
// missing from registry yields *UnknownDatastoreError together with the
// unchanged config.
func ApplyDatastore(
	config map[string]any, policyType string, refs []aigw.DatastoreRef,
	registry map[string]*aigw.Datastore,
) (map[string]any, error) {
	if len(refs) == 0 {
		return config, nil
	}
	if !PolicyTypeSupportsDatastore(policyType) {
		return nil, fmt.Errorf("plugin type %q does not support datastores", policyType)
	}
	if len(refs) > 1 {
		return nil, errors.New("a policy may reference at most one datastore")
	}
	datastore := registry[refs[0].Name]
	if datastore == nil {
		return config, &UnknownDatastoreError{Name: refs[0].Name}
	}
	datastoreType, datastoreConfig := datastore.Type, datastore.Config
	support, allowed := DatastoreSupportForPolicyType(policyType, datastoreType)
	if !allowed {
		return nil, fmt.Errorf("plugin type %q does not support a %q datastore",
			policyType, datastoreType)
	}
	switch support.ConfigPath {
	case "vectordb":
		strategy, _ := VectorDBStrategyForDatastoreType(datastoreType)
		vectordbConfig, _ := config["vectordb"].(map[string]any)
		return applyVectorDBDatastore(config, vectordbConfig, strategy, datastoreConfig), nil
	case "redis":
		return applyRedisDatastore(config, datastoreConfig), nil
	case "storage_config.redis":
		return applyAcmeDatastore(config, datastoreConfig), nil
	case "resources.cache.redis":
		return applyDatakitDatastore(config, datastoreConfig), nil
	default:
		return nil, fmt.Errorf("unhandled Datastore config path %q", support.ConfigPath)
	}
}

// applyRedisDatastore assigns dsConfig at config["redis"] — the flat,
// no-strategy case shared by most of DatastoreSupport's plugins
// (rate-limiting, ai-rate-limiting-advanced, proxy-cache-advanced, etc.).
// Never mutates config in place, so a reusable source Policy is never
// mutated.
func applyRedisDatastore(config map[string]any, dsConfig map[string]any) map[string]any {
	out := make(map[string]any, len(config)+1)
	maps.Copy(out, config)
	out["redis"] = dsConfig
	return out
}

// applyAcmeDatastore assigns dsConfig at config["storage_config"]["redis"] —
// acme's one dot-path, one level deeper than the flat "redis" case. Never
// mutates config or its nested storage_config in place, so a reusable source
// Policy is never mutated.
func applyAcmeDatastore(config map[string]any, dsConfig map[string]any) map[string]any {
	out := make(map[string]any, len(config)+1)
	maps.Copy(out, config)
	storageConfig, _ := out["storage_config"].(map[string]any)
	newStorageConfig := make(map[string]any, len(storageConfig)+1)
	maps.Copy(newStorageConfig, storageConfig)
	newStorageConfig["redis"] = dsConfig
	out["storage_config"] = newStorageConfig
	return out
}

// applyDatakitDatastore assigns dsConfig at
// config["resources"]["cache"]["redis"] — datakit's one dot-path, two levels
// deeper than the flat "redis" case. Never mutates config or its nested
// resources/cache in place, so a reusable source Policy is never mutated.
func applyDatakitDatastore(config map[string]any, dsConfig map[string]any) map[string]any {
	out := make(map[string]any, len(config)+1)
	maps.Copy(out, config)
	resources, _ := out["resources"].(map[string]any)
	newResources := make(map[string]any, len(resources)+1)
	maps.Copy(newResources, resources)
	cache, _ := newResources["cache"].(map[string]any)
	newCache := make(map[string]any, len(cache)+1)
	maps.Copy(newCache, cache)
	newCache["redis"] = dsConfig
	newResources["cache"] = newCache
	out["resources"] = newResources
	return out
}

// applyVectorDBDatastore merges dsConfig into pluginConfig's vectordb block
// at strategy — already validated by the caller (a recognized, allowed
// Datastore type resolving to strategy, with vectordb.strategy consistent
// with it). The connection sub-block named by strategy (config.vectordb.redis
// or .pgvector) is replaced wholesale; every other key already in
// vectordbConfig — the common fields (strategy, dimensions, distance_metric,
// threshold) a policy or model authors itself, per VectorDBDatastoreConfig's
// own doc comment in the source API spec — is left untouched. Never mutates
// pluginConfig or vectordbConfig in place, so a reusable source Policy is
// never mutated.
func applyVectorDBDatastore(
	pluginConfig, vectordbConfig map[string]any, strategy string, dsConfig map[string]any,
) map[string]any {
	out := make(map[string]any, len(pluginConfig)+1)
	maps.Copy(out, pluginConfig)
	out["vectordb"] = mergeVectorDBDatastore(vectordbConfig, strategy, dsConfig)
	return out
}

// mergeVectorDBDatastore returns a copy of vectordbConfig with strategy set
// and the connection sub-block named by strategy replaced wholesale with
// dsConfig. vectordbConfig may be nil, in which case the block is built from
// the datastore alone. Never mutates its inputs.
func mergeVectorDBDatastore(vectordbConfig map[string]any, strategy string, dsConfig map[string]any) map[string]any {
	const newVectorDBKeys = 2 // "strategy" and the redis/pgvector sub-block, both set below.
	vectordb := make(map[string]any, len(vectordbConfig)+newVectorDBKeys)
	maps.Copy(vectordb, vectordbConfig)
	vectordb["strategy"] = strategy
	vectordb[strategy] = dsConfig
	return vectordb
}

// modelDatastoreTypes are the Datastore types a semantic-balancer model can
// reference: the same set the VectorDB policy family accepts — redis-ee or
// vectordb, never redis-ce.
var modelDatastoreTypes = map[string]struct{}{
	DatastoreTypeRedisEE:  {},
	DatastoreTypeVectorDB: {},
}

// ApplyModelDatastore substitutes a Datastore's connection config into a
// model's vectordb block that VectorDBToPlugin has ALREADY lowered to plugin
// shape — dsConfig carries flat plugin keys (ssl_verify, sentinel_nodes, ...)
// and must not be lowered again. It rejects a Datastore type no model accepts;
// the strategy (and with it the connection sub-block) always follows the
// Datastore's type, while the common fields the model authors itself
// (dimensions, distance_metric, threshold) are preserved.
func ApplyModelDatastore(vectordb any, datastoreType string, dsConfig map[string]any) (any, error) {
	supportedDatastoreTypes := slices.Collect(maps.Keys(modelDatastoreTypes))
	if _, ok := modelDatastoreTypes[datastoreType]; !ok {
		return nil, fmt.Errorf("a model does not support a %q datastore; use one of %v instead",
			datastoreType, supportedDatastoreTypes)
	}
	strategy, _ := VectorDBStrategyForDatastoreType(datastoreType)
	block, _ := vectordb.(map[string]any)
	return mergeVectorDBDatastore(block, strategy, dsConfig), nil
}
