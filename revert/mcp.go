package revert

import (
	"fmt"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertMCPServer lifts a service route carrying ai-mcp-proxy back into an AI
// Gateway MCP Server: config.mode becomes the type, the plugin's embedded tools
// come back out as Tools, the ACL config (acl_attribute_type /
// access_token_claim_field / default_acl) becomes config.access, and any other
// route- or service-level plugins become policies. plugins is the route's
// effective plugin list (nested route plugins plus service-level plugins).
func (r *Reverter) revertMCPServer(svc *kong.Service, rt *kong.Route, plugins []kong.Plugin) error {
	mcpPlugin := findPlugin(plugins, "ai-mcp-proxy")
	cfg := mcpPlugin.Config

	labels := r.tagsToLabels(mcpPlugin.Tags)
	if len(labels) == 0 {
		labels = r.tagsToLabels(svc.Tags)
	}

	// Several routes of one service can each carry ai-mcp-proxy, so the service
	// name alone can collide. Keep svc.Name when unique (preserving the common
	// single-route naming), otherwise prefer the distinct route name, then a
	// numeric suffix.
	name := r.uniqueMCPName(svc.Name, rt.Name)

	m := aigw.MCPServer{
		Type:   getStr(cfg, "mode"),
		Name:   name,
		Labels: labels,
	}
	if m.Type == "" {
		if err := r.warn("MCP server %q: ai-mcp-proxy has no mode; defaulting to listener", svc.Name); err != nil {
			return err
		}
		m.Type = "listener"
	}

	// The forward converter uses a placeholder localhost host when the server
	// has no upstream; reverse that back to "no config.url".
	if svc.URL != "" || svc.Host != placeholderHost {
		m.Config.URL = serviceURL(svc)
	}

	m.Config.Route = routeConfig(rt, name)
	m.Config.MaxRequestBodySize = getInt(cfg, "max_request_body_size")
	m.Config.Logging = loggingFromBlockWithDefaults(getMap(cfg, "logging"), true, false)
	m.Config.Server = getMap(cfg, "server")
	m.Config.Proxy = proxyFromConfig(getMap(cfg, "proxy_config"))
	m.Config.ToolsCacheTTLSeconds = getInt(cfg, "tools_cache_ttl_seconds")

	// Access: the ACL attribute config and default_acl live in the plugin
	// config; lift them back into the structured config.access block.
	attrType := getStr(cfg, "acl_attribute_type")
	claimField := getStr(cfg, "access_token_claim_field")
	var defaultToolACLs aigw.ACLs
	if dacl := getSlice(cfg, "default_acl"); len(dacl) > 0 {
		if block, ok := dacl[0].(map[string]any); ok {
			defaultToolACLs = aclsFromBlock(block)
		}
		if len(dacl) > 1 {
			if err := r.warn(
				"MCP server %q: only the first default_acl entry is convertible; %d dropped",
				svc.Name, len(dacl)-1); err != nil {
				return err
			}
		}
	}
	if attrType != "" || claimField != "" || !defaultToolACLs.IsEmpty() {
		m.Config.Access = &aigw.MCPConfigAccess{
			ACLAttributeType:      attrType,
			AccessTokenClaimField: claimField,
			DefaultToolACLs:       defaultToolACLs,
		}
	}

	// The AI Gateway schema requires every tool to carry a non-empty name
	// (both conversion and listener tool variants). Source ai-mcp-proxy configs
	// often leave it null, so synthesize a stable, unique name when missing.
	seenToolNames := map[string]bool{}
	for _, raw := range getSlice(cfg, "tools") {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		t := mcpTool(tool)
		if t.Name == "" {
			t.Name = uniqueToolName(mcpToolName(tool), seenToolNames)
		}
		seenToolNames[t.Name] = true
		m.Tools = append(m.Tools, t)
	}

	refs, acls := r.policyRefs(plugins, "mcp-servers")
	m.Policies = refs
	m.Access.ACLs = acls

	r.out.MCPServers = append(r.out.MCPServers, m)
	return nil
}

// uniqueMCPName returns a collision-free MCP server name. It keeps svcName when
// unused, otherwise falls back to the distinct routeName, then to a numeric
// suffix, recording the chosen name so later servers avoid it.
func (r *Reverter) uniqueMCPName(svcName, routeName string) string {
	pick := func(name string) (string, bool) {
		if name == "" || r.mcpNames[name] {
			return "", false
		}
		r.mcpNames[name] = true
		return name, true
	}
	if name, ok := pick(svcName); ok {
		return name
	}
	if name, ok := pick(routeName); ok {
		return name
	}
	base := svcName
	if base == "" {
		base = routeName
	}
	for i := 2; ; i++ {
		if name, ok := pick(fmt.Sprintf("%s-%d", base, i)); ok {
			return name
		}
	}
}

// mcpTool reverses one ai-mcp-proxy config.tools[] entry.
func mcpTool(tool map[string]any) aigw.MCPTool {
	t := aigw.MCPTool{
		Name:         getStr(tool, "name"),
		Description:  getStr(tool, "description"),
		Method:       getStr(tool, "method"),
		Path:         getStr(tool, "path"),
		Scheme:       getStr(tool, "scheme"),
		Host:         getStr(tool, "host"),
		Headers:      getMap(tool, "headers"),
		Query:        getMap(tool, "query"),
		RequestBody:  getMap(tool, "request_body"),
		Responses:    getMap(tool, "responses"),
		Annotations:  getMap(tool, "annotations"),
		InputSchema:  getMap(tool, "input_schema"),
		OutputSchema: getMap(tool, "output_schema"),
	}
	for _, raw := range getSlice(tool, "parameters") {
		if p, ok := raw.(map[string]any); ok {
			t.Parameters = append(t.Parameters, p)
		}
	}
	if acl := getMap(tool, "acl"); acl != nil {
		t.Access.ACLs = aclsFromBlock(acl)
	}
	return t
}

// mcpToolName derives a tool name for a source tools[] entry whose name is
// empty. The AI Gateway schema treats name as an override for
// annotations.title, so prefer that; otherwise fall back to the HTTP
// method+path. The result is slugified into a valid identifier.
func mcpToolName(tool map[string]any) string {
	if title := getStr(getMap(tool, "annotations"), "title"); title != "" {
		return slugify(title)
	}
	method := getStr(tool, "method")
	path := getStr(tool, "path")
	if s := slugify(strings.TrimSpace(method + " " + path)); s != "" {
		return s
	}
	return "tool"
}

// uniqueToolName ensures name does not collide with any previously assigned
// tool name in seen, appending -2, -3, … as needed.
func uniqueToolName(name string, seen map[string]bool) string {
	if name == "" {
		name = "tool"
	}
	if !seen[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !seen[candidate] {
			return candidate
		}
	}
}

// slugify lowercases s and replaces every run of non-alphanumeric characters
// with a single dash, trimming leading/trailing dashes.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true // trim leading dashes
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
