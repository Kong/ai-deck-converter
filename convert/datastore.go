package convert

import "github.com/Kong/ai-deck-converter/internal/aimap"

// DatastoreSupported is exported for Konnect's control plane, which rejects
// an unsupported pairing at write time. ApplyDatastore already refuses one,
// but only for a policy the conversion emits — and a scoped policy is emitted
// only once some entity references it, so a control plane validating a policy
// on its own would otherwise store a reference that fails at delivery.
func DatastoreSupported(policyType, datastoreType string) bool {
	_, allowed := aimap.DatastoreSupportForPolicyType(policyType, datastoreType)
	return allowed
}

// DatastorePathsForPolicyType is exported for Konnect's control plane, which
// scrubs these paths from a policy carrying a Datastore reference before
// storing it: Kong's own defaults would otherwise persist a connection the
// conversion re-substitutes anyway.
func DatastorePathsForPolicyType(policyType string) []string {
	support, ok := aimap.DatastoreSupportForPluginType(policyType)
	if !ok {
		return nil
	}
	if support.ConfigPath != aimap.VectorDBConfigPath {
		return []string{support.ConfigPath}
	}
	return []string{
		support.ConfigPath + "." + aimap.VectorDBStrategyRedis,
		support.ConfigPath + "." + aimap.VectorDBStrategyPGVector,
	}
}

func DatastoreStrategyPathForPolicyType(policyType string) string {
	support, ok := aimap.DatastoreSupportForPluginType(policyType)
	if !ok {
		return ""
	}
	return support.StrategyPath
}

func DatastoreStrategyForDatastoreType(datastoreType string) (string, bool) {
	return aimap.VectorDBStrategyForDatastoreType(datastoreType)
}
