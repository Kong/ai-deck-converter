package convert

import (
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// aclFields enumerates the acl-plugin config fields an AI Gateway ACL can
// populate, in schema order. The Kong acl plugin declares all four mutually
// exclusive. Only the two with cel false have an ai-mcp-proxy counterpart: its
// default_acl entries and tools[].acl records define allow and deny alone, and
// Kong rejects an unknown record key outright.
var aclFields = []struct {
	name   string
	cel    bool
	values func(aigw.ACLs) []string
}{
	{"allow", false, func(a aigw.ACLs) []string { return a.Allow }},
	{"deny", false, func(a aigw.ACLs) []string { return a.Deny }},
	{"allow_when", true, func(a aigw.ACLs) []string { return a.AllowWhen }},
	{"deny_when", true, func(a aigw.ACLs) []string { return a.DenyWhen }},
}

// aclConfig builds a config map from the ACL fields that pass keep. Returns nil
// when no field is populated.
func aclConfig(acls aigw.ACLs, keep func(cel bool) bool) map[string]any {
	config := map[string]any{}
	for _, f := range aclFields {
		if !keep(f.cel) {
			continue
		}
		if values := f.values(acls); len(values) > 0 {
			config[f.name] = values
		}
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

// aclBlock builds the {allow, deny} config map every consumer accepts. Returns
// nil when both lists are empty. The CEL when-expressions live on the acl
// plugin alone, so aclPlugin builds its own config.
func aclBlock(acls aigw.ACLs) map[string]any {
	return aclConfig(acls, func(cel bool) bool { return !cel })
}

// hasCELExpressions reports whether an ACL carries CEL when-expressions.
func hasCELExpressions(acls aigw.ACLs) bool {
	for _, f := range aclFields {
		if f.cel && len(f.values(acls)) > 0 {
			return true
		}
	}
	return false
}

// celFieldPaths returns the path of each CEL field that acls sets. Each path
// starts with prefix.
func celFieldPaths(prefix string, acls aigw.ACLs) []string {
	var paths []string
	for _, f := range aclFields {
		if f.cel && len(f.values(acls)) > 0 {
			paths = append(paths, prefix+"."+f.name)
		}
	}
	return paths
}

// aclFieldsSet names the acl-plugin config fields an ACL populates, in schema
// order. More than one name means the ACL has no valid single-plugin
// representation, since the four fields are mutually exclusive.
func aclFieldsSet(acls aigw.ACLs) []string {
	var set []string
	for _, f := range aclFields {
		if len(f.values(acls)) > 0 {
			set = append(set, f.name)
		}
	}
	return set
}

// mergeACLs unions two AI Gateway ACLs' lists, base first, so a broader ACL (a
// server-wide acls block) and a narrower one (default_tool_acls) both apply
// instead of one shadowing the other. Duplicate subjects are kept as-authored;
// Kong tolerates them.
func mergeACLs(base, extra aigw.ACLs) aigw.ACLs {
	if extra.IsEmpty() {
		return base
	}
	if base.IsEmpty() {
		return extra
	}
	return aigw.ACLs{
		Allow:     slices.Concat(base.Allow, extra.Allow),
		Deny:      slices.Concat(base.Deny, extra.Deny),
		AllowWhen: slices.Concat(base.AllowWhen, extra.AllowWhen),
		DenyWhen:  slices.Concat(base.DenyWhen, extra.DenyWhen),
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

// aclPlugin builds a Kong acl plugin from an AI Gateway ACL. It carries every
// field, CEL when-expressions included.
//
// include_consumer_groups is always set: AI Gateway's only group-membership
// construct is consumer_groups (the converter never creates the legacy
// per-consumer kong.db.acls rows the acl plugin checks by default), so
// allow/deny entries that name a consumer_groups group would otherwise never
// match anything and the plugin could never allow a request.
func aclPlugin(acls aigw.ACLs) kong.Plugin {
	config := aclConfig(acls, func(bool) bool { return true })
	if config == nil {
		config = map[string]any{}
	}
	config["include_consumer_groups"] = true
	return kong.Plugin{Name: "acl", Config: config}
}
