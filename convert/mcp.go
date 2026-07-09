package convert

import (
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
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
		// Honor enabled: false, consistent with agents (convert/agent.go) and policies (convert/policy.go).
		// Without this, an MCP server an operator disabled still lowers to an active service + route +
		// ai-mcp-proxy plugin and keeps serving on every data plane.
		if m.Enabled != nil && !*m.Enabled {
			service.Enabled = m.Enabled
		}
		// In conversion-only/conversion-listener mode, a tool with no per-tool host
		// override is meant to dispatch against this MCP server's own Gateway
		// Service (per AIGatewayMCPConversionTool's own doc string: "By default,
		// Kong will extract the host from API configuration"). At runtime,
		// ai-mcp-proxy (kong/plugins/ai-mcp-proxy/tools.lua) resolves that by
		// re-entering Kong's own router with the tool's raw method+path, which
		// only reaches this Service if some Route actually matches that path. The
		// listener route above is keyed on the server's own config.route path
		// (e.g. /mcp/echo), not the tool's path, so it never matches. Emit a
		// plain, plugin-less companion Route per such tool so the self-dispatch
		// has a Route to match. Not needed for listener (no upstream to dispatch
		// to at all), passthrough-listener (forwards to its own already-matching
		// route, no per-tool synthesis), or upstream-server (tools are fetched
		// from, and dispatched to, the real backend MCP server directly).
		if m.Type == "conversion-only" || m.Type == "conversion-listener" {
			for i := range m.Tools {
				t := &m.Tools[i]
				if t.Host == "" && strings.HasPrefix(t.Path, "/") {
					service.Routes = append(service.Routes, mcpToolRoute(m.Name, t))
				}
			}
		}
		c.out.Services = append(c.out.Services, service)
	}
	return nil
}

// mcpToolRoute builds a plain, plugin-less companion Route for an MCP
// conversion tool with no per-tool host override, giving the ai-mcp-proxy
// plugin's own-Service self-dispatch (tools.lua) a real Route to match on the
// tool's raw method+path. strip_path is forced false: Kong's default (true)
// would strip the matched prefix before proxying, mangling the outbound
// request the plugin expects to reach the backend unchanged.
//
// The route carries aimap.MCPToolRouteTag so revert/service.go can recognize
// it as converter-owned plumbing (fully recoverable from the co-located
// ai-mcp-proxy plugin's own tools[] config) rather than an unrelated
// hand-authored route it must preserve as a separate Agent. MCP tools have no
// labels field in the schema, so there are no user tags to keep alongside it.
func mcpToolRoute(serverName string, t *aigw.MCPTool) kong.Route {
	return kong.Route{
		Name:      serverName + "-" + t.Name + "-route",
		Paths:     []string{mcpToolRoutePathPrefix(t.Path)},
		Methods:   []string{t.Method},
		StripPath: boolPtr(false),
		Tags:      []string{aimap.MCPToolRouteTag},
	}
}

// mcpToolRoutePathPrefix derives a Kong route path prefix from an MCP tool's
// path. Kong's default path matching is prefix-based, and the plugin computes
// the actual outbound request path from the tool's raw path separately, so
// this only needs to give the router something to match: a templated path
// (e.g. /resource/{id}) is truncated at its first template placeholder.
func mcpToolRoutePathPrefix(path string) string {
	if idx := strings.Index(path, "{"); idx >= 0 {
		path = path[:idx]
	}
	if trimmed := strings.TrimSuffix(path, "/"); trimmed != "" {
		path = trimmed
	}
	return path
}

func (c *Converter) mcpPlugin(m *aigw.MCPServer) (kong.Plugin, error) {
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
	// proxy_config is honored by the plugin only in passthrough-listener mode,
	// but we pass it through whenever set and let the plugin validate.
	setIfNotEmptyMap(cfg, "proxy_config", m.Config.Proxy)
	// tools_cache_ttl_seconds is required by the plugin in upstream-server mode.
	if m.Config.ToolsCacheTTLSeconds != nil {
		cfg["tools_cache_ttl_seconds"] = *m.Config.ToolsCacheTTLSeconds
	}
	// Auth: emit the ACL attribute config and default_acl. Prefer the structured
	// config.auth block; fall back to the top-level acls/default_tool_acls (the
	// legacy input shape). default_acl prefers default_tool_acls over acls.
	if a := m.Config.Auth; a != nil {
		setIfNotEmpty(cfg, "acl_attribute_type", a.ACLAttributeType)
		setIfNotEmpty(cfg, "access_token_claim_field", a.AccessTokenClaimField)
		if acl := defaultACLBlock(a.DefaultToolACLs); acl != nil {
			cfg["default_acl"] = acl
		} else if acl := defaultACLBlock(a.ACLs); acl != nil {
			cfg["default_acl"] = acl
		}
	} else if acl := defaultACLBlock(m.DefaultToolACLs); acl != nil {
		cfg["default_acl"] = acl
	} else if acl := defaultACLBlock(m.ACLs); acl != nil {
		cfg["default_acl"] = acl
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
			if err := c.warn("MCP server %q tool %q has no description; ai-mcp-proxy requires one", serverName, t.Name); err != nil {
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
		if t.ACLs != nil {
			if acl := aclBlock(*t.ACLs); acl != nil {
				tool["acl"] = acl
			}
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
