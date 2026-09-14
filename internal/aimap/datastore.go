package aimap

// Datastore type discriminators (aigw.Datastore.Type values). Named here,
// where they're actually validated and interpreted, so nothing compares
// against a bare string literal.
const (
	DatastoreTypeRedisCE  = "redis-ce"
	DatastoreTypeRedisEE  = "redis-ee"
	DatastoreTypeVectorDB = "vectordb" // despite the name, always pgvector; see datastoreTypeVectorDBMap.
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
	DatastoreTypeRedisCE:  "redis",
	DatastoreTypeRedisEE:  "redis",
	DatastoreTypeVectorDB: "pgvector",
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

// DatastoreSupportForPolicyType returns how the given policy plugin type
// consumes a Datastore, and whether datastoreType is one it accepts. An
// unrecognized policyType returns a zero DatastoreSupport (ConfigPath == "")
// and allowed == false.
func DatastoreSupportForPolicyType(policyType, datastoreType string) (support DatastoreSupport, allowed bool) {
	support, ok := datastoreSupportedPolicyTypes[policyType]
	if !ok {
		return DatastoreSupport{}, false
	}
	return support, support.Allows(datastoreType)
}
