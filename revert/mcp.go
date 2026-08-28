package revert

import (
	"fmt"
	"sort"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// indexMCPListenerSources reconstructs the listener/source relationship the
// forward converter encodes purely as tags (see convert.wireListenerSources): a
// listener's ai-mcp-proxy carries a config.server.tag bucket selector, and each
// source's ai-mcp-proxy plugin carries that same value in its tags. It records
// every bucket tag (so revertMCPServer can strip it from source labels, since
// it is a data-plane detail rather than a user label) and, for each listener,
// the source MCP servers whose plugin tags join its bucket.
func (r *Reverter) indexMCPListenerSources() {
	listenerByTag := map[string]string{}
	type mcpEntry struct {
		service string
		tags    []string
	}
	var entries []mcpEntry
	for i := range r.src.Services {
		svc := &r.src.Services[i]
		for j := range svc.Routes {
			mcp := findPlugin(r.routePlugins(&svc.Routes[j]), "ai-mcp-proxy")
			if mcp == nil {
				continue
			}
			entries = append(entries, mcpEntry{service: svc.Name, tags: mcp.Tags})
			if getStr(mcp.Config, "mode") != "listener" {
				continue
			}
			if tag := getStr(getMap(mcp.Config, "server"), "tag"); tag != "" {
				listenerByTag[tag] = svc.Name
				r.mcpBucketTags[tag] = true
			}
		}
	}
	for _, e := range entries {
		for _, tag := range e.tags {
			listener, ok := listenerByTag[tag]
			if !ok || listener == e.service {
				continue
			}
			r.mcpSources[listener] = append(r.mcpSources[listener], e.service)
		}
	}
	for listener := range r.mcpSources {
		sort.Strings(r.mcpSources[listener])
	}
}

// stripBucketTags removes listener bucket selectors from a tag list so they are
// not misread as user labels; see indexMCPListenerSources.
func (r *Reverter) stripBucketTags(tags []string) []string {
	if len(r.mcpBucketTags) == 0 {
		return tags
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if !r.mcpBucketTags[t] {
			out = append(out, t)
		}
	}
	return out
}

// revertMCPServer lifts a service route carrying ai-mcp-proxy back into an AI
// Gateway MCP Server: config.mode becomes the type, the plugin's embedded tools
// come back out as Tools, the ACL config (acl_attribute_type /
// access_token_claim_field / default_acl) becomes the server-level access
// block, and any other route- or service-level plugins become policies.
func (r *Reverter) revertMCPServer(svc *kong.Service, rt *kong.Route, plugins, svcPlugins []kong.Plugin) error {
	mcpPlugin := findPlugin(plugins, "ai-mcp-proxy")
	cfg := mcpPlugin.Config

	labels := r.tagsToLabels(r.stripBucketTags(mcpPlugin.Tags))
	if len(labels) == 0 {
		labels = r.tagsToLabels(r.stripBucketTags(svc.Tags))
	}

	m := aigw.MCPServer{
		ID:     getStr(cfg, "mcp_server_id"),
		Type:   getStr(cfg, "mode"),
		Name:   svc.Name,
		Labels: labels,
	}
	// The forward converter maps a disabled MCP server to a disabled Gateway
	// Service; lift that back so the round trip preserves enabled: false. Mirror
	// the forward guard (only carry the flag when explicitly disabled) to keep
	// the two directions symmetric.
	if svc.Enabled != nil && !*svc.Enabled {
		m.Enabled = svc.Enabled
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
	m.Config.Logging = loggingFromBlockWithDefaults(getMap(cfg, "logging"), true, false)
	m.Config.Server = mcpServerConfigForAIGateway(getMap(cfg, "server"))
	m.Config.Sources = r.mcpSources[svc.Name]
	m.Config.Proxy = proxyFromConfig(getMap(cfg, "proxy_config"))
	upstream, err := r.upstreamFromConfig(getMap(cfg, "auth"), fmt.Sprintf("MCP server %q", svc.Name))
	if err != nil {
		return err
	}
	m.Config.Upstream = upstream
	m.Config.ToolsCacheTTLSeconds = getInt(cfg, "tools_cache_ttl_seconds")

	// Access: the ACL attribute config and default_acl live in the plugin
	// config; lift them back into the server-level access block.
	m.Access.ACLAttributeType = getStr(cfg, "acl_attribute_type")
	m.Access.AccessTokenClaimField = getStr(cfg, "access_token_claim_field")
	if dacl := getSlice(cfg, "default_acl"); len(dacl) > 0 {
		if block, ok := dacl[0].(map[string]any); ok {
			m.Access.DefaultToolACLs = aclsFromBlock(block)
		}
		if len(dacl) > 1 {
			if err := r.warn(
				"MCP server %q: only the first default_acl entry is convertible; %d dropped",
				svc.Name, len(dacl)-1); err != nil {
				return err
			}
		}
	}

	for _, raw := range getSlice(cfg, "tools") {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		m.Tools = append(m.Tools, mcpTool(tool))
	}

	// Pull auth-strategy / OAuth 2.0 Protected Resource Metadata access out
	// of the route plugins (into access.auth_strategies/metadata) before the
	// remaining plugins are reconstructed as policies.
	allPlugins := append(append([]kong.Plugin{}, plugins...), svcPlugins...)
	rest := r.revertMCPAccess(&m, allPlugins)

	refs, acls := r.policyRefs(rest)
	m.Policies = refs
	m.Access.ACLs = acls

	r.out.MCPServers = append(r.out.MCPServers, m)
	return nil
}

// mcpServerConfigForAIGateway returns a copy of the ai-mcp-proxy plugin's
// server block for the AI Gateway MCP server config, without mutating the
// parsed decK config. server.tag (the listener bucket selector) is carried
// through as-is, matching the forward direction.
func mcpServerConfigForAIGateway(server map[string]any) map[string]any {
	if server == nil {
		return nil
	}
	config := make(map[string]any, len(server))
	for key, value := range server {
		config[key] = value
	}
	return config
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
