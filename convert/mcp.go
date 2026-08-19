package convert

import (
	"fmt"
	"slices"

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
		route.Source = source("mcp_server", m.Name, "config.route")
		plugin, err := c.mcpPlugin(m)
		if err != nil {
			return err
		}
		plugin.Source = source("mcp_server", m.Name, "config",
			kong.FieldMapping{GeneratedPrefix: "config.mode", SourcePrefix: "type"},
			kong.FieldMapping{GeneratedPrefix: "config.tools", SourcePrefix: "tools"},
			kong.FieldMapping{GeneratedPrefix: "config.proxy_config", SourcePrefix: "config.proxy"},
			kong.FieldMapping{GeneratedPrefix: "config.auth", SourcePrefix: "config.upstream.auth"},
			kong.FieldMapping{GeneratedPrefix: "config.default_acl", SourcePrefix: "access"},
			kong.FieldMapping{GeneratedPrefix: "config.acl_attribute_type", SourcePrefix: "access.acl_attribute_type"},
			kong.FieldMapping{
				GeneratedPrefix: "config.access_token_claim_field",
				SourcePrefix:    "access.access_token_claim_field",
			},
			kong.FieldMapping{GeneratedPrefix: "config.server.tag", SourcePrefix: "config.server.tag"},
		)
		route.Plugins = append(route.Plugins, plugin)

		// Non-ACL policy plugins still apply at the route; ACLs are folded into
		// the ai-mcp-proxy plugin above.
		guard, err := c.scopedPlugins(entityMCPServer, m.Policies, aigw.ACLs{})
		if err != nil {
			return err
		}
		guard = sourceScopedPlugins(guard, "mcp_server", m.Name)
		route.Plugins = append(route.Plugins, guard...)

		// Identity-provider / OAuth 2.0 Protected Resource Metadata access.
		// Emits an ai-mcp-oauth2 plugin (openid-connect + metadata) or a plain
		// auth plugin (no metadata), and appends the metadata endpoint path to
		// the route so one route serves both MCP traffic and the .well-known
		// metadata.
		authPlugins, err := c.mcpIdentityPlugins(m, &route)
		if err != nil {
			return err
		}
		route.Plugins = append(route.Plugins, authPlugins...)

		service := kong.Service{
			Name:   m.Name,
			Routes: []kong.Route{route},
			Tags:   c.labelsToTags(m.Labels),
			Source: serviceURLSource("mcp_server", m.Name),
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
	c.wireListenerSources()
	return nil
}

// wireListenerSources implements the listener/source relationship. A `listener`
// MCP server exposes the tools of the source MCP servers named in its
// config.sources. On the DP this is expressed with tags: the listener plugin's
// server.tag (set by the CP from the listener id) selects a bucket, and each
// source plugin contributes its tools to that bucket via a matching entry in its
// own tags. So for every listener we take its server.tag and add it to the tags
// of each referenced source's ai-mcp-proxy plugin.
//
// A source referenced by more than one listener accumulates one tag per listener
// (it belongs to several buckets). A referenced source that is absent from the
// document (e.g. write-time validation of a single listener) is skipped.
func (c *Converter) wireListenerSources() {
	// Index each MCP server's ai-mcp-proxy plugin by service name. Pointers into
	// c.out.Services are stable now that every service has been appended.
	pluginByServer := make(map[string]*kong.Plugin)
	for si := range c.out.Services {
		svc := &c.out.Services[si]
		for ri := range svc.Routes {
			route := &svc.Routes[ri]
			for pi := range route.Plugins {
				if route.Plugins[pi].Name == "ai-mcp-proxy" {
					pluginByServer[svc.Name] = &route.Plugins[pi]
				}
			}
		}
	}

	for i := range c.src.MCPServers {
		m := &c.src.MCPServers[i]
		if m.Type != "listener" || len(m.Config.Sources) == 0 {
			continue
		}
		tag := listenerTag(m.Config.Server)
		if tag == "" {
			continue
		}
		for _, sourceName := range m.Config.Sources {
			plugin, ok := pluginByServer[sourceName]
			if !ok {
				continue
			}
			plugin.Tags = addTag(plugin.Tags, tag)
		}
	}
}

// listenerTag returns the listener bucket selector from a server config. It
// prefers server.tag (what the CP sets and what convert now carries through),
// falling back to server.label so reverted documents and older hand-written
// configs — which still use server.label — keep wiring listener/source
// relationships and round-tripping correctly.
func listenerTag(server map[string]any) string {
	if tag, _ := server["tag"].(string); tag != "" {
		return tag
	}
	label, _ := server["label"].(string)
	return label
}

// addTag appends tag to tags if absent, keeping the result sorted so conversion
// output is deterministic regardless of listener/source ordering.
func addTag(tags []string, tag string) []string {
	if slices.Contains(tags, tag) {
		return tags
	}
	tags = append(tags, tag)
	slices.Sort(tags)
	return tags
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
		cfg["server"] = mcpServerConfigForPlugin(m.Config.Server)
	}
	// proxy_config is honored by the plugin only in passthrough-listener mode,
	// but we pass it through whenever set and let the plugin validate.
	if pc := proxyConfigBlock(m.Config.Proxy); pc != nil {
		cfg["proxy_config"] = pc
	}
	// Upstream authentication (e.g. AWS SigV4) lowers to the plugin's auth
	// record; only emitted when set.
	auth, err := c.upstreamAuthBlock(m.Config.Upstream, fmt.Sprintf("MCP server %q", m.Name))
	if err != nil {
		return kong.Plugin{}, err
	}
	if auth != nil {
		cfg["auth"] = auth
	}
	// tools_cache_ttl_seconds is required by the plugin in upstream-server mode.
	if m.Config.ToolsCacheTTLSeconds != nil {
		cfg["tools_cache_ttl_seconds"] = *m.Config.ToolsCacheTTLSeconds
	}
	// Access: emit the ACL attribute config and default_acl. Merges the
	// server-wide acls with default_tool_acls (acls first) so both apply rather
	// than one shadowing the other.
	setIfNotEmpty(cfg, "acl_attribute_type", m.Access.ACLAttributeType)
	setIfNotEmpty(cfg, "access_token_claim_field", m.Access.AccessTokenClaimField)
	if acl := defaultACLBlock(mergeACLs(m.Access.ACLs, m.Access.DefaultToolACLs)); acl != nil {
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
	if m.Access.ACLAttributeType != "oauth_access_token" {
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

// mcpServerConfigForPlugin returns a shallow copy of the MCP server's server
// config for the ai-mcp-proxy plugin, so plugin-side handling never mutates the
// source document. server.tag (the listener bucket selector) is carried through
// as-is; the CP sets it from the listener id and wireListenerSources propagates
// it to the referenced source plugins' tags.
func mcpServerConfigForPlugin(server map[string]any) map[string]any {
	config := make(map[string]any, len(server))
	for key, value := range server {
		config[key] = value
	}
	return config
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
