package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// revertMCPServer lifts a service route carrying ai-mcp-proxy back into an AI
// Gateway MCP Server: config.mode becomes the type, the plugin's embedded tools
// come back out as Tools, the ACL config (acl_attribute_type /
// access_token_claim_field / default_acl) becomes config.auth, and any other
// route- or service-level plugins become policies.
func (r *Reverter) revertMCPServer(svc *kong.Service, rt *kong.Route, plugins, svcPlugins []kong.Plugin) error {
	cfg := findPlugin(plugins, "ai-mcp-proxy").Config

	m := aigw.MCPServer{
		Type:   getStr(cfg, "mode"),
		Name:   svc.Name,
		Labels: r.tagsToLabels(svc.Tags),
	}
	if m.Type == "" {
		if err := r.warn("MCP server %q: ai-mcp-proxy has no mode; defaulting to listener", svc.Name); err != nil {
			return err
		}
		m.Type = "listener"
	}

	// The forward converter uses a placeholder localhost host when the server
	// has no upstream; reverse that back to "no upstream_url".
	if svc.URL != "" || svc.Host != placeholderHost {
		m.UpstreamURL = serviceURL(svc)
	}

	m.Config.Route = routeConfig(rt, svc.Name)
	m.Config.MaxRequestBodySize = getInt(cfg, "max_request_body_size")
	m.Config.Logging = loggingFromBlock(getMap(cfg, "logging"))
	m.Config.Server = getMap(cfg, "server")
	m.Config.Proxy = getMap(cfg, "proxy_config")
	m.Config.ToolsCacheTTLSeconds = getInt(cfg, "tools_cache_ttl_seconds")

	// Auth: the ACL attribute config and default_acl live in the plugin config;
	// lift them back into the structured config.auth block.
	attrType := getStr(cfg, "acl_attribute_type")
	claimField := getStr(cfg, "access_token_claim_field")
	var defaultToolACLs aigw.ACLs
	if dacl := getSlice(cfg, "default_acl"); len(dacl) > 0 {
		if block, ok := dacl[0].(map[string]any); ok {
			defaultToolACLs = aclsFromBlock(block)
		}
		if len(dacl) > 1 {
			if err := r.warn("MCP server %q: only the first default_acl entry is convertible; %d dropped", svc.Name, len(dacl)-1); err != nil {
				return err
			}
		}
	}
	if attrType != "" || claimField != "" || !defaultToolACLs.IsEmpty() {
		m.Config.Auth = &aigw.MCPAuth{
			ACLAttributeType:      attrType,
			AccessTokenClaimField: claimField,
			DefaultToolACLs:       defaultToolACLs,
		}
	}

	for _, raw := range getSlice(cfg, "tools") {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		m.Tools = append(m.Tools, mcpTool(tool))
	}

	refs, acls := r.policyRefs(append(append([]kong.Plugin{}, plugins...), svcPlugins...))
	m.Policies = refs
	m.ACLs = acls

	r.out.MCPServers = append(r.out.MCPServers, m)
	return nil
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
		acls := aclsFromBlock(acl)
		if !acls.IsEmpty() {
			t.ACLs = &acls
		}
	}
	return t
}
