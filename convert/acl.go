package convert

import (
	"github.com/gperanich/ai-deck-converter/internal/aigw"
	"github.com/gperanich/ai-deck-converter/internal/kong"
)

// aclBlock builds an {allow, deny} config map from an AI Gateway ACL. Returns
// nil when both lists are empty. Reused by the Kong acl plugin (for Models,
// Agents) and by the ai-mcp-proxy plugin's internal ACL fields.
func aclBlock(acls aigw.ACLs) map[string]any {
	if acls.IsEmpty() {
		return nil
	}
	config := map[string]any{}
	if len(acls.Allow) > 0 {
		config["allow"] = acls.Allow
	}
	if len(acls.Deny) > 0 {
		config["deny"] = acls.Deny
	}
	return config
}

// aclPlugin builds a Kong acl plugin from an AI Gateway ACL allow/deny list.
func aclPlugin(acls aigw.ACLs) kong.Plugin {
	return kong.Plugin{Name: "acl", Config: aclBlock(acls)}
}
