package convert

import (
	"github.com/gperanich/ai-deck-converter/internal/aigw"
	"github.com/gperanich/ai-deck-converter/internal/kong"
)

// convertMCPServers translates AI Gateway MCP Servers into a Gateway Service +
// Route with an ai-mcp-proxy plugin (config.mode = the source type). MCP ACLs
// live inside the plugin config (default_acl / tools[].acl), not as Kong acl
// plugins, because ai-mcp-proxy does not support consumer scoping.
func (c *Converter) convertMCPServers() error {
	for i := range c.src.MCPServers {
		m := &c.src.MCPServers[i]
		route := buildRoute(m.Config.Route, m.Name)
		route.Plugins = append(route.Plugins, c.mcpPlugin(m))

		// Non-ACL policy plugins still apply at the route; ACLs are folded into
		// the ai-mcp-proxy plugin above.
		guard, err := c.scopedPlugins(m.Policies, aigw.ACLs{})
		if err != nil {
			return err
		}
		route.Plugins = append(route.Plugins, guard...)

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
				if err := c.warn("MCP server %q is passthrough-listener but has no upstream_url; using placeholder host %q", m.Name, placeholderHost); err != nil {
					return err
				}
			}
		}
		c.out.Services = append(c.out.Services, service)
	}
	return nil
}

func (c *Converter) mcpPlugin(m *aigw.MCPServer) kong.Plugin {
	cfg := map[string]any{"mode": m.Type}
	if m.Config.MaxRequestBodySize != nil {
		cfg["max_request_body_size"] = *m.Config.MaxRequestBodySize
	}
	if logging := loggingBlock(m.Config.Logging); logging != nil {
		cfg["logging"] = logging
	}
	if len(m.Config.Server) > 0 {
		cfg["server"] = m.Config.Server
	}
	// default_acl: prefer the explicit default_tool_acls, fall back to server acls.
	if acl := aclBlock(m.DefaultToolACLs); acl != nil {
		cfg["default_acl"] = acl
	} else if acl := aclBlock(m.ACLs); acl != nil {
		cfg["default_acl"] = acl
	}
	if tools := mcpTools(m.Tools); tools != nil {
		cfg["tools"] = tools
	}
	return kong.Plugin{Name: "ai-mcp-proxy", Config: cfg}
}

func mcpTools(tools []aigw.MCPTool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for i := range tools {
		t := &tools[i]
		tool := map[string]any{"name": t.Name}
		setIfNotEmpty(tool, "description", t.Description)
		setIfNotEmpty(tool, "method", t.Method)
		setIfNotEmpty(tool, "path", t.Path)
		setIfNotEmpty(tool, "scheme", t.Scheme)
		setIfNotEmpty(tool, "host", t.Host)
		setIfNotEmptyMap(tool, "headers", t.Headers)
		setIfNotEmptyMap(tool, "query", t.Query)
		setIfNotEmptyMap(tool, "request_body", t.RequestBody)
		setIfNotEmptyMap(tool, "responses", t.Responses)
		setIfNotEmptyMap(tool, "parameters", t.Parameters)
		setIfNotEmptyMap(tool, "annotations", t.Annotations)
		if t.ACLs != nil {
			if acl := aclBlock(*t.ACLs); acl != nil {
				tool["acl"] = acl
			}
		}
		out = append(out, tool)
	}
	return out
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
