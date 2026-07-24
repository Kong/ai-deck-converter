package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// convertMCPServers translates AI Gateway MCP Servers into a Gateway Service +
// Route with an ai-mcp-proxy plugin (config.mode = the source type, one of
// conversion-only | conversion-listener | listener | passthrough-listener |
// upstream-server). MCP ACLs live inside the plugin config (default_acl /
// tools[].acl), not as Kong acl plugins, because ai-mcp-proxy does not support
// consumer scoping.
func (c *Converter) convertMCPServers() error {
	for i := range c.src.MCPServers {
		m := &c.src.MCPServers[i]
		route := buildRoute(m.Config.Route, m.Name)
		plugin, err := c.mcpPlugin(m)
		if err != nil {
			return err
		}
		route.Plugins = append(route.Plugins, plugin)

		// Non-ACL policy plugins still apply at the route; ACLs are folded into
		// the ai-mcp-proxy plugin above.
		guard, err := c.scopedPlugins(entityMCPServer, m.Policies, aigw.ACLs{})
		if err != nil {
			return err
		}
		route.Plugins = append(route.Plugins, guard...)

		// upstream-server's tools (manually declared and/or fetched from its own upstream_url) are only
		// ever surfaced through a listener-mode MCP server whose config.server.tag matches a Kong tag
		// derived from this server's labels (ai-mcp-proxy tools.lua: tag-bucketing in
		// merge_all_remote_tools, then get_tools_list's listener-only lookup). With no labels at all,
		// this entry can never be tagged, so no listener can ever discover or call its tools — the
		// server loads and deploys cleanly but is permanently unreachable by any client.
		if m.Type == "upstream-server" && len(m.Labels) == 0 {
			if err := c.warn("MCP server %q is upstream-server but has no labels; without a matching tag on a listener-mode MCP server, its tools can never be discovered or called", m.Name); err != nil {
				return err
			}
		}

		service := kong.Service{
			Name:   m.Name,
			Routes: []kong.Route{route},
			Tags:   c.labelsToTags(m.Labels),
		}
		if m.UpstreamURL != "" {
			service.URL = m.UpstreamURL
		} else {
			service.Host = placeholderHost
			if m.Type == "passthrough-listener" {
				if err := c.warn(
					"MCP server %q is passthrough-listener but has no upstream_url; using placeholder host %q",
					m.Name, placeholderHost); err != nil {
					return err
				}
			}
		}
		// Honor enabled: false, consistent with agents (convert/agent.go) and policies (convert/policy.go).
		// Without this, an MCP server an operator disabled still lowers to an active service + route +
		// ai-mcp-proxy plugin and keeps serving on every data plane.
		if m.Enabled != nil && !*m.Enabled {
			service.Enabled = m.Enabled
		}
		c.out.Services = append(c.out.Services, service)
	}
	return nil
}

func (c *Converter) mcpPlugin(m *aigw.MCPServer) (kong.Plugin, error) {
	cfg := map[string]any{"mode": m.Type}
	if m.Config.MaxRequestBodySize != nil {
		cfg["max_request_body_size"] = *m.Config.MaxRequestBodySize
	}
	if logging := loggingBlock(withLoggingDefaults(m.Config.Logging, true, false)); logging != nil {
		cfg["logging"] = logging
	}
	if len(m.Config.Server) > 0 {
		cfg["server"] = m.Config.Server
	}
	// proxy_config is honored by the plugin only in passthrough-listener mode,
	// but we pass it through whenever set and let the plugin validate.
	if pc := proxyConfigBlock(m.Config.Proxy); pc != nil {
		cfg["proxy_config"] = pc
	}
	// tools_cache_ttl_seconds is required by the plugin in upstream-server mode.
	if m.Config.ToolsCacheTTLSeconds != nil {
		cfg["tools_cache_ttl_seconds"] = *m.Config.ToolsCacheTTLSeconds
	}
	// Access: emit the ACL attribute config and default_acl. Prefer the
	// structured config.access block; fall back to the server-level access
	// block. default_acl prefers default_tool_acls over acls. Merges the server-wide acls with
	// default_tool_acls (acls first) so both apply rather than one shadowing the
	// other.
	if a := m.Config.Access; a != nil {
		setIfNotEmpty(cfg, "acl_attribute_type", a.ACLAttributeType)
		setIfNotEmpty(cfg, "access_token_claim_field", a.AccessTokenClaimField)
		if acl := defaultACLBlock(mergeACLs(a.ACLs, a.DefaultToolACLs)); acl != nil {
			cfg["default_acl"] = acl
		}
	} else if acl := defaultACLBlock(mergeACLs(m.Access.ACLs, m.Access.DefaultToolACLs)); acl != nil {
		cfg["default_acl"] = acl
	}
	// include_consumer_groups is set by default, mirroring aclPlugin() in convert/acl.go: AI Gateway's
	// only group-membership construct is consumer_groups (the converter never creates the legacy
	// per-consumer kong.db.acls rows the classic acl plugin checks by default; ai-mcp-proxy's own
	// subjects.lua ACL-subject extraction has the identical gap, defaulting to false), so allow/deny
	// entries naming a consumer_groups group would otherwise never match anything. Exception:
	// when acl_attribute_type is oauth_access_token, the plugin's schema hard-rejects
	// include_consumer_groups being set (and subjects.lua ignores it in that mode regardless), so
	// leave it unset there.
	if m.Config.Access == nil || m.Config.Access.ACLAttributeType != "oauth_access_token" {
		cfg["include_consumer_groups"] = true
	}
	tools, err := c.mcpTools(m.Name, m.Tools)
	if err != nil {
		return kong.Plugin{}, err
	}
	if tools != nil {
		cfg["tools"] = tools
	}
	return kong.Plugin{
		Name:   "ai-mcp-proxy",
		Config: cfg,
		Tags:   c.labelsToTags(m.Labels),
	}, nil
}

func (c *Converter) mcpTools(serverName string, tools []aigw.MCPTool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for i := range tools {
		t := &tools[i]
		tool := map[string]any{"name": t.Name}
		if t.Description == "" {
			if err := c.warn(
				"MCP server %q tool %q has no description; ai-mcp-proxy requires one",
				serverName, t.Name); err != nil {
				return nil, err
			}
		}
		setIfNotEmpty(tool, "description", t.Description)
		setIfNotEmpty(tool, "method", t.Method)
		setIfNotEmpty(tool, "path", t.Path)
		setIfNotEmpty(tool, "scheme", t.Scheme)
		setIfNotEmpty(tool, "host", t.Host)
		setIfNotEmptyMap(tool, "headers", t.Headers)
		setIfNotEmptyMap(tool, "query", t.Query)
		setIfNotEmptyMap(tool, "request_body", t.RequestBody)
		setIfNotEmptyMap(tool, "responses", t.Responses)
		if len(t.Parameters) > 0 {
			tool["parameters"] = t.Parameters
		}
		setIfNotEmptyMap(tool, "annotations", t.Annotations)
		setIfNotEmptyMap(tool, "input_schema", t.InputSchema)
		setIfNotEmptyMap(tool, "output_schema", t.OutputSchema)
		if acl := aclBlock(t.Access.ACLs); acl != nil {
			tool["acl"] = acl
		}
		out = append(out, tool)
	}
	return out, nil
}

func setIfNotEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

func setIfNotEmptyMap(m map[string]any, key string, val map[string]any) {
	if len(val) > 0 {
		m[key] = val
	}
}
