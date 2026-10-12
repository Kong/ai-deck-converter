package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// aclBlock builds an {allow, deny} config map from an AI Gateway ACL. Returns
// nil when both lists are empty. This is the shape every consumer accepts:
// the ai-mcp-proxy plugin's default_acl entries and tools[].acl records define
// only allow and deny, and Kong rejects an unknown record key outright. The
// CEL when-expressions live on the acl plugin alone, so aclPlugin adds them.
func aclBlock(acls aigw.ACLs) map[string]any {
	config := map[string]any{}
	if len(acls.Allow) > 0 {
		config["allow"] = acls.Allow
	}
	if len(acls.Deny) > 0 {
		config["deny"] = acls.Deny
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

// hasCELExpressions reports whether an ACL carries CEL when-expressions.
func hasCELExpressions(acls aigw.ACLs) bool {
	return len(acls.AllowWhen) > 0 || len(acls.DenyWhen) > 0
}

// aclFieldsSet names the acl-plugin config fields an ACL populates, in schema
// order. The Kong acl plugin declares all four mutually exclusive, so more
// than one name means the ACL has no valid single-plugin representation.
func aclFieldsSet(acls aigw.ACLs) []string {
	var set []string
	if len(acls.Allow) > 0 {
		set = append(set, "allow")
	}
	if len(acls.Deny) > 0 {
		set = append(set, "deny")
	}
	if len(acls.AllowWhen) > 0 {
		set = append(set, "allow_when")
	}
	if len(acls.DenyWhen) > 0 {
		set = append(set, "deny_when")
	}
	return set
}

// mergeACLs unions two AI Gateway ACLs' allow and deny lists, base first, so a
// broader ACL (a server-wide acls block) and a narrower one (default_tool_acls)
// both apply instead of one shadowing the other. Duplicate subjects are kept
// as-authored; Kong tolerates them.
func mergeACLs(base, extra aigw.ACLs) aigw.ACLs {
	if extra.IsEmpty() {
		return base
	}
	if base.IsEmpty() {
		return extra
	}
	return aigw.ACLs{
		Allow:     append(append([]string{}, base.Allow...), extra.Allow...),
		Deny:      append(append([]string{}, base.Deny...), extra.Deny...),
		AllowWhen: append(append([]string{}, base.AllowWhen...), extra.AllowWhen...),
		DenyWhen:  append(append([]string{}, base.DenyWhen...), extra.DenyWhen...),
	}
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
	// The acl plugin is the only consumer whose schema defines the CEL
	// when-expressions.
	if len(acls.AllowWhen) > 0 {
		config["allow_when"] = acls.AllowWhen
	}
	if len(acls.DenyWhen) > 0 {
		config["deny_when"] = acls.DenyWhen
	}
	config["include_consumer_groups"] = true
	return kong.Plugin{Name: "acl", Config: config}
}
