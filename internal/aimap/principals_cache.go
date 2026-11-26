package aimap

import (
	"maps"

	"github.com/Kong/ai-deck-converter/internal/aigw"
)

// AuthStrategyDatastoreTypes are the Datastore types that an auth strategy
// accepts. It takes at most one of each type.
var AuthStrategyDatastoreTypes = map[string]struct{}{
	DatastoreTypeTokenVault: {},
	DatastoreTypeRedisEE:    {},
}

// MaxAuthStrategyDatastores is the number of Datastores that an auth strategy
// can reference.
const MaxAuthStrategyDatastores = 2

// PrincipalsCacheStrategyRedis is the principals_cache.strategy value that
// selects the redis-ee Datastore of an openid-connect strategy.
const PrincipalsCacheStrategyRedis = "redis"

// ApplyPrincipalsCacheRedis returns a copy of a key-auth config with the
// redis-ee Datastore at principals_cache.redis. It returns config unchanged
// when the config has no principals_cache. It never changes config.
func ApplyPrincipalsCacheRedis(config map[string]any, ds *aigw.Datastore) map[string]any {
	return withPrincipalsCache(config, func(cache, _ map[string]any) {
		cache["redis"] = ds.Config
	})
}

// ApplyPrincipalsCacheRedisTopLevel returns a copy of an openid-connect config
// with the redis-ee Datastore at config.redis. It also sets
// principals_cache.strategy to redis. It returns config unchanged when the
// config has no principals_cache. It never changes config.
func ApplyPrincipalsCacheRedisTopLevel(config map[string]any, ds *aigw.Datastore) map[string]any {
	return withPrincipalsCache(config, func(cache, out map[string]any) {
		cache["strategy"] = PrincipalsCacheStrategyRedis
		out["redis"] = ds.Config
	})
}

func withPrincipalsCache(config map[string]any, apply func(cache, out map[string]any)) map[string]any {
	cache, ok := config["principals_cache"].(map[string]any)
	if !ok {
		return config
	}
	cacheOut := make(map[string]any, len(cache)+1)
	maps.Copy(cacheOut, cache)
	out := make(map[string]any, len(config)+1)
	maps.Copy(out, config)
	out["principals_cache"] = cacheOut
	apply(cacheOut, out)
	return out
}
