package aimap

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
	"redis-ce": "redis",
	"redis-ee": "redis",
	"vectordb": "pgvector",
}

// VectorDBForDatastoreType returns the vectordb plugin sub-block name a
// Datastore of the given type supplies connection config for, and whether the
// type is recognized.
func VectorDBForDatastoreType(datastoreType string) (string, bool) {
	group, ok := datastoreTypeVectorDBMap[datastoreType]
	return group, ok
}
