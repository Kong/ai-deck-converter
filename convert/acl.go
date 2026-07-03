package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
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

// defaultACLBlock wraps an AI Gateway ACL into the ai-mcp-proxy config.default_acl
// array shape: a list of {scope, allow, deny} entries. Returns nil when empty.
// scope is left unset (Kong defaults it to "tools") per the omit-defaults policy.
func defaultACLBlock(acls aigw.ACLs) []map[string]any {
	block := aclBlock(acls)
	if block == nil {
		return nil
	}
	return []map[string]any{block}
}

// aclPlugin builds a Kong acl plugin from an AI Gateway ACL allow/deny list.
//
// include_consumer_groups is always set: AI Gateway's only group-membership
// construct is consumer_groups (the converter never creates the legacy
// per-consumer kong.db.acls rows the acl plugin checks by default), so
// allow/deny entries that name a consumer_groups group would otherwise never
// match anything and the plugin could never allow a request.
func aclPlugin(acls aigw.ACLs) kong.Plugin {
	config := aclBlock(acls)
	if config == nil {
		config = map[string]any{}
	}
	config["include_consumer_groups"] = true
	return kong.Plugin{Name: "acl", Config: config}
}
