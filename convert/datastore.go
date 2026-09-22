package convert

import "github.com/Kong/ai-deck-converter/internal/aimap"

// DatastorePathsForPolicyType is exported for Konnect's control plane, which
// scrubs these paths from a policy carrying a Datastore reference before
// storing it: Kong's own defaults would otherwise persist a connection the
// conversion re-substitutes anyway.
func DatastorePathsForPolicyType(policyType string) []string {
	if !aimap.PolicyTypeSupportsDatastore(policyType) {
		return nil
	}

	support, _ := aimap.DatastoreSupportForPolicyType(policyType, "")
	if support.ConfigPath != aimap.VectorDBConfigPath {
		return []string{support.ConfigPath}
	}
	return []string{
		support.ConfigPath + "." + aimap.VectorDBStrategyRedis,
		support.ConfigPath + "." + aimap.VectorDBStrategyPGVector,
	}
}
