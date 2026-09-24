package convert

import (
	"fmt"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// mcpConversionOnly is the MCP server mode that serves no MCP traffic of its
// own: it is a toolset exposed through the listeners that name it in
// config.sources. Its route is closed to clients by a gate plugin (see
// mcpToolsetGate).
const mcpConversionOnly = "conversion-only"

// convertMCPServers translates AI Gateway MCP Servers into a Gateway Service +
// Route with an ai-mcp-proxy plugin (config.mode = the source type, one of
// conversion-only | conversion-listener | listener | passthrough-listener |
// upstream-server). MCP ACLs live inside the plugin config (default_acl /
// tools[].acl), not as Kong acl plugins, because ai-mcp-proxy does not support
// consumer scoping.
func (c *Converter) convertMCPServers() error {
	// Which entries of c.out.Services belong to a conversion-only server, held
	// by index, not by name, so pruning can never remove a service that merely
	// shares a name with one (the shared model service is named
	// aimap.GatewayServiceName, which an MCP server is free to be called too).
	var conversionOnlyServices []int
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
			kong.FieldMapping{GeneratedPrefix: "config.auth.token_vault", SourcePrefix: "token_vault"},
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

		// Auth-strategy / OAuth 2.0 Protected Resource Metadata access.
		// Emits an ai-mcp-oauth2 plugin (openid-connect + metadata) or a plain
		// auth plugin (no metadata), and appends the metadata endpoint path to
		// the route so one route serves both MCP traffic and the .well-known
		// metadata.
		authPlugins, err := c.mcpIdentityPlugins(m, &route)
		if err != nil {
			return err
		}
		route.Plugins = append(route.Plugins, authPlugins...)
		if m.Type == mcpConversionOnly {
			// Kong allows one plugin per name on a route, so a policy of the
			// gate's type would collide with it; neither can be dropped without
			// either opening the route or losing the user's plugin.
			if slices.ContainsFunc(route.Plugins, func(p kong.Plugin) bool {
				return p.Name == aimap.MCPToolsetGatePlugin
			}) {
				return c.failAt("policies",
					"MCP server %q is conversion-only, so its route carries a generated %q gate; "+
						"it cannot also have a %q policy",
					m.Name, aimap.MCPToolsetGatePlugin, aimap.MCPToolsetGatePlugin)
			}
			route.Plugins = append(route.Plugins, mcpToolsetGate(m))
		}

		service := kong.Service{
			Name:   m.Name,
			ID:     m.ID,
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
		if m.Type == mcpConversionOnly {
			conversionOnlyServices = append(conversionOnlyServices, len(c.out.Services))
		}
		c.out.Services = append(c.out.Services, service)
	}
	c.wireListenerSources()
	return c.pruneUnexposedSources(conversionOnlyServices)
}

// mcpToolsetGate closes a conversion-only server's route to clients. Such a
// server serves no MCP traffic of its own and cannot declare access itself
// (mcpIdentityPlugins rejects auth on non-listener modes): its tools are only
// meant to be reached through the listeners that name it in config.sources.
// Without the gate its route would be reachable directly, on terms none of
// those listeners set -- bypassing their auth and ACLs alike.
//
// ai-mcp-proxy executes a listener's tool call by re-entering Kong's own proxy
// over a unix socket, so the gate lets exactly those requests through and
// answers 404 to everything else, hiding the route rather than advertising it
// with a 401. The plugin is tagged so revert recognizes it as generated (see
// aimap.MCPToolsetGateTag).
func mcpToolsetGate(m *aigw.MCPServer) kong.Plugin {
	return kong.Plugin{
		Name:   aimap.MCPToolsetGatePlugin,
		Config: aimap.MCPToolsetGateConfig(),
		Tags:   []string{aimap.MCPToolsetGateTag},
		Source: source("mcp_server", m.Name, "type"),
	}
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
		tag, _ := m.Config.Server["tag"].(string)
		if tag == "" {
			continue
		}
		for _, sourceName := range m.Config.Sources {
			if plugin, ok := pluginByServer[sourceName]; ok {
				plugin.Tags = addTag(plugin.Tags, tag)
			}
		}
	}
}

// pruneUnexposedSources drops conversion-only MCP servers that no listener in
// the document names in config.sources. Such a server reaches no client — its
// tools are only ever served through a listener — so its Service and Route
// exist solely as dead configuration: its route is gated off from clients
// (mcpToolsetGate), and no listener re-enters it to execute its tools.
//
// Note this is a whole-document judgement. A conversion-only server converted
// on its own, with its listener in another document, has nothing here to
// associate with and is pruned.
func (c *Converter) pruneUnexposedSources(conversionOnlyServices []int) error {
	exposed := map[string]bool{}
	for i := range c.src.MCPServers {
		m := &c.src.MCPServers[i]
		if m.Type != "listener" {
			continue
		}
		for _, sourceName := range m.Config.Sources {
			exposed[sourceName] = true
		}
	}

	// Collect first, in document order, so the warnings are deterministic.
	drop := make(map[int]bool, len(conversionOnlyServices))
	for _, i := range conversionOnlyServices {
		name := c.out.Services[i].Name
		if exposed[name] {
			continue
		}
		if err := c.warn(
			"MCP server %q is conversion-only but no listener names it in config.sources; "+
				"dropping its service and route", name); err != nil {
			return err
		}
		drop[i] = true
	}
	if len(drop) == 0 {
		return nil
	}

	kept := make([]kong.Service, 0, len(c.out.Services)-len(drop))
	for i := range c.out.Services {
		if drop[i] {
			continue
		}
		kept = append(kept, c.out.Services[i])
	}
	c.out.Services = kept
	return nil
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
	// Upstream authentication (e.g. AWS SigV4) and Token Vault credential
	// resolution both lower to the plugin's auth record, which can only carry
	// one provider — a server declaring both cannot be represented.
	if m.TokenVault != nil && m.Config.Upstream != nil && m.Config.Upstream.Auth != nil {
		return kong.Plugin{}, c.failAt("token_vault",
			"MCP server %q: token_vault and config.upstream.auth are mutually exclusive "+
				"(both lower to the ai-mcp-proxy plugin's auth record)", m.Name)
	}
	// Kong's Token Vault caches exchanged credentials in Redis encrypted with
	// encryption_secrets, so a redis block without them is rejected by the
	// plugin's own entity check. Reject here rather than emit a config decK
	// would refuse to apply.
	if tv := m.TokenVault; tv != nil && tv.Redis != nil && len(tv.EncryptionSecrets) == 0 {
		return kong.Plugin{}, c.failAt("token_vault.encryption_secrets",
			"MCP server %q: token_vault.encryption_secrets is required when token_vault.redis is configured", m.Name)
	}
	// A present but empty token_vault block would otherwise lower to
	// auth.token_vault: {} — the plugin schema requires directory and provider
	// when provider is token_vault, so reject it the same way. (After this the
	// lowered block can never be empty.)
	if tv := m.TokenVault; tv != nil && (tv.Directory == "" || tv.Provider == "") {
		return kong.Plugin{}, c.failAt("token_vault",
			"MCP server %q: token_vault requires directory and provider", m.Name)
	}
	// Token Vault credential resolution (auth.provider: token_vault); only
	// emitted when set.
	if tv := m.TokenVault; tv != nil {
		cfg["auth"] = map[string]any{
			"provider":    aimap.UpstreamAuthProviderTokenVault,
			"token_vault": aimap.TokenVaultToPlugin(tv),
		}
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
