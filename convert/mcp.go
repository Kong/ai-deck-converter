package convert

import (
	"sort"
	"strings"

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

		// In conversion modes, ai-mcp-proxy runs a tools/call by re-dispatching the
		// tool's REST request back through Kong's router; without a route matching
		// the tool path that dispatch 404s. Emit a companion route so it resolves.
		routes := []kong.Route{route}
		companion, err := c.mcpToolsRoute(m)
		if err != nil {
			return err
		}
		if companion != nil {
			routes = append(routes, *companion)
		}

		service := kong.Service{
			Name:   m.Name,
			Routes: routes,
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

// mcpToolsRoute synthesizes the "companion" route backing a server's tool REST
// paths so ai-mcp-proxy's runtime tools/call re-dispatch resolves against a real
// route instead of 404ing. Only conversion-only / conversion-listener translate
// tools into REST calls that re-enter Kong's router; the other modes proxy to an
// upstream MCP server, so they need no companion route. Returns nil when the
// server yields no usable tool paths.
//
// A single route carrying every distinct static tool-path prefix is emitted
// (one deterministic, collision-free name; all tools on a server share the same
// service/upstream). It is left unscoped on methods/hosts/protocols so it matches
// every tool's resolved request, and strip_path is false so the full REST path
// reaches the upstream.
func (c *Converter) mcpToolsRoute(m *aigw.MCPServer) (*kong.Route, error) {
	if m.Type != "conversion-only" && m.Type != "conversion-listener" {
		return nil, nil
	}
	paths, err := c.toolRoutePaths(m)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return &kong.Route{
		Name:      m.Name + "-tools-route",
		Paths:     paths,
		StripPath: boolPtr(false),
	}, nil
}

// toolRoutePaths returns the sorted, de-duplicated static path prefixes for a
// server's tools. Empty and relative paths are skipped silently: a relative tool
// path is a legitimate ai-mcp-proxy feature (the plugin prepends the route path),
// not a REST path that needs its own route. An absolute path whose leading
// segment is templated would collapse to "/" (matching everything), so it is
// warned about and skipped instead.
func (c *Converter) toolRoutePaths(m *aigw.MCPServer) ([]string, error) {
	seen := map[string]bool{}
	var paths []string
	for i := range m.Tools {
		t := &m.Tools[i]
		if t.Path == "" || !strings.HasPrefix(t.Path, "/") {
			continue
		}
		prefix, ok := staticPathPrefix(t.Path)
		if !ok {
			if err := c.warn(
				"MCP server %q tool %q path %q begins with a template segment; "+
					"no companion route synthesized (the tool call may 404)",
				m.Name, t.Name, t.Path); err != nil {
				return nil, err
			}
			continue
		}
		if !seen[prefix] {
			seen[prefix] = true
			paths = append(paths, prefix)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// staticPathPrefix reduces a tool REST path to the leading run of static
// segments, stopping at the first templated ("{...}") or empty segment, since
// Kong route paths cannot express path templates. It returns ok=false when the
// path yields no static segment (e.g. "/" or "/{version}/x").
func staticPathPrefix(p string) (string, bool) {
	var static []string
	for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
		if seg == "" || strings.ContainsAny(seg, "{}") {
			break
		}
		static = append(static, seg)
	}
	if len(static) == 0 {
		return "", false
	}
	return "/" + strings.Join(static, "/"), true
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
