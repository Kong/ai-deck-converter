package revert

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// registerAuthStrategy dedupes a key-auth/openid-connect plugin into the auth
// strategy registry: a plugin with the same type and config (minus the
// synthesized "anonymous" fallback, which convert always adds) reuses the
// existing auth strategy; otherwise a new one is registered under a unique
// name.
//
// idpType is the plugin the clone copies, because a cloned plugin reverts as the
// base plugin and the AI Gateway model has no clone type.
func (r *Reverter) registerAuthStrategy(p kong.Plugin, idpType string) *aigw.AuthStrategy {
	cfg := stripSyntheticAuthFields(idpType, p.Config)
	cfg, datastores := r.liftPrincipalsCacheRedis(idpType, cfg)

	for i := range r.authStrategies {
		existing := &r.authStrategies[i]
		if existing.Type != idpType {
			continue
		}
		if !reflect.DeepEqual(existing.Config, cfg) || !reflect.DeepEqual(existing.Datastores, datastores) {
			continue
		}
		return existing
	}

	idp := aigw.AuthStrategy{
		Type:       idpType,
		Name:       r.uniqueAuthStrategyName(idpType),
		Config:     cfg,
		Datastores: datastores,
	}
	r.authStrategies = append(r.authStrategies, idp)
	return &r.authStrategies[len(r.authStrategies)-1]
}

// uniqueAuthStrategyName derives a stable, human-readable name of the
// form "<type>-<n>".
func (r *Reverter) uniqueAuthStrategyName(idpType string) string {
	var name string
	for {
		r.authStrategyCounts[idpType]++
		candidate := fmt.Sprintf("%s-%d", idpType, r.authStrategyCounts[idpType])
		if !r.authStrategyNames[candidate] {
			name = candidate
			break
		}
	}
	r.authStrategyNames[name] = true
	return name
}

// syntheticAuthFields lists, per auth plugin type, the config keys that never
// belong on the AI Gateway auth strategy: they are synthesized by the forward
// converter or injected by the data plane, not authored by the user.
//   - anonymous: convert always sets it as the fallback consumer marker.
//   - identity_realms / realm: DP-defaulted on key-auth (convert re-emits
//     identity_realms via forceKeyAuthIdentityRealms when principals is
//     enabled, so stripping it here still round-trips byte-identically).
var syntheticAuthFields = map[string][]string{
	"key-auth":       {"anonymous", "identity_realms", "realm"},
	"openid-connect": {"anonymous"},
}

// stripSyntheticAuthFields returns a copy of an auth plugin's config with the
// synthetic/DP-injected keys for its type removed, so they do not surface on
// the reconstructed AI Gateway auth strategy.
func stripSyntheticAuthFields(pluginName string, config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	drop := make(map[string]bool, 3) //nolint:mnd
	for _, k := range syntheticAuthFields[pluginName] {
		drop[k] = true
	}
	out := make(map[string]any, len(config))
	for k, v := range config {
		if drop[k] {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// withoutHiddenAccessTokenHeader returns cfg without the
// upstream_access_token_header null that convert adds to agent and MCP
// openid-connect plugins that hide credentials.
func withoutHiddenAccessTokenHeader(cfg map[string]any) map[string]any {
	if v, set := cfg["upstream_access_token_header"]; !set || v != nil {
		return cfg
	}
	if hide, ok := cfg["hide_credentials"].(bool); ok && !hide {
		return cfg
	}
	out := maps.Clone(cfg)
	delete(out, "upstream_access_token_header")
	return out
}

// liftPrincipalsCacheRedis moves the principals cache connection of an auth
// plugin config into a redis-ee Datastore. It returns the config without the
// connection and a reference to the Datastore. Convert puts the connection at
// principals_cache.redis for key-auth. It puts it at redis for openid-connect,
// with principals_cache.strategy set to redis. Other configs stay as they are.
func (r *Reverter) liftPrincipalsCacheRedis(idpType string, cfg map[string]any) (map[string]any, []aigw.DatastoreRef) {
	cache, ok := cfg["principals_cache"].(map[string]any)
	if !ok {
		return cfg, nil
	}
	out := maps.Clone(cfg)
	cacheOut := maps.Clone(cache)
	out["principals_cache"] = cacheOut

	var connection map[string]any
	switch idpType {
	case "key-auth":
		connection, _ = cache["redis"].(map[string]any)
		delete(cacheOut, "redis")
	case "openid-connect":
		if getStr(cache, "strategy") != aimap.PrincipalsCacheStrategyRedis {
			return cfg, nil
		}
		connection, _ = cfg["redis"].(map[string]any)
		delete(cacheOut, "strategy")
		delete(out, "redis")
	}
	if connection == nil {
		return cfg, nil
	}
	return out, []aigw.DatastoreRef{{Name: r.redisDatastoreName(connection)}}
}

// redisDatastoreName adds a redis-ee Datastore for the connection and returns
// its name. Equal connections share one Datastore.
func (r *Reverter) redisDatastoreName(connection map[string]any) string {
	for _, ds := range r.out.Datastores {
		if ds.Type == aimap.DatastoreTypeRedisEE && reflect.DeepEqual(ds.Config, connection) {
			return ds.Name
		}
	}
	name := aimap.DatastoreTypeRedisEE
	for n := 2; r.datastoreNameUsed(name); n++ {
		name = fmt.Sprintf("%s-%d", aimap.DatastoreTypeRedisEE, n)
	}
	r.out.Datastores = append(r.out.Datastores, aigw.Datastore{
		Name: name, Type: aimap.DatastoreTypeRedisEE, Config: connection,
	})
	return name
}

func (r *Reverter) datastoreNameUsed(name string) bool {
	return slices.ContainsFunc(r.out.Datastores, func(ds aigw.Datastore) bool { return ds.Name == name })
}
