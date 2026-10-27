package convert

import (
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// aclFields lists the ACL fields in schema order. The Kong acl plugin permits
// exactly one of them. An MCP ACL sets allow and deny, or allow_when and
// deny_when.
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

// aclConfig builds a config map from the ACL fields. Returns nil when no field
// is populated.
func aclConfig(acls aigw.ACLs) map[string]any {
	config := map[string]any{}
	for _, f := range aclFields {
		if values := f.values(acls); len(values) > 0 {
			config[f.name] = values
		}
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

// aclFieldPaths returns prefix plus the name of each field that acls sets.
func aclFieldPaths(prefix string, acls aigw.ACLs) []string {
	return fieldPaths(prefix, acls, false)
}

// celFieldPaths returns prefix plus the name of each CEL field that acls sets.
func celFieldPaths(prefix string, acls aigw.ACLs) []string {
	return fieldPaths(prefix, acls, true)
}

func fieldPaths(prefix string, acls aigw.ACLs, celOnly bool) []string {
	var paths []string
	for _, f := range aclFields {
		if (f.cel || !celOnly) && len(f.values(acls)) > 0 {
			paths = append(paths, prefix+f.name)
		}
	}
	return paths
}

// mixesACLPairs reports whether acls sets a field of the allow/deny pair and
// a field of the allow_when/deny_when pair.
func mixesACLPairs(acls aigw.ACLs) bool {
	return len(acls.Allow)+len(acls.Deny) > 0 && len(acls.AllowWhen)+len(acls.DenyWhen) > 0
}

// mergeACLs joins each list of base and extra, base first. Duplicate subjects
// stay.
func mergeACLs(base, extra aigw.ACLs) aigw.ACLs {
	return aigw.ACLs{
		Allow:     slices.Concat(base.Allow, extra.Allow),
		Deny:      slices.Concat(base.Deny, extra.Deny),
		AllowWhen: slices.Concat(base.AllowWhen, extra.AllowWhen),
		DenyWhen:  slices.Concat(base.DenyWhen, extra.DenyWhen),
	}
}

// defaultACLBlock wraps an AI Gateway ACL into the ai-mcp-proxy config.default_acl
// array shape: a list of {scope, allow, deny, allow_when, deny_when} entries.
// Returns nil when empty. scope is left unset (Kong defaults it to "tools") per
// the omit-defaults policy.
func defaultACLBlock(acls aigw.ACLs) []map[string]any {
	block := aclConfig(acls)
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
	config := aclConfig(acls)
	if config == nil {
		config = map[string]any{}
	}
	config["include_consumer_groups"] = true
	return kong.Plugin{Name: "acl", Config: config}
}
