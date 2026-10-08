package aimap

import "slices"

// MCP toolset gate: a conversion-only MCP server is a toolset served only
// through the listeners that name it in config.sources, never to clients
// directly. ai-mcp-proxy executes a listener's tool call by re-entering Kong's
// own proxy over a unix socket, so the forward direction guards every
// conversion-only route with a pre-function that answers 404 to anything that
// did not arrive that way. The reverse direction recognizes the plugin by its
// tag and drops it, since it is derived from the server type rather than
// declared by the user.
const (
	// MCPToolsetGatePlugin is the Kong plugin the gate is emitted as.
	MCPToolsetGatePlugin = "pre-function"
	// MCPToolsetGateTag marks the gate as converter-generated.
	MCPToolsetGateTag = "aigw-generated:mcp-toolset-gate"
	// MCPToolsetGateAccess is the gate's access-phase Lua: only requests
	// served on a unix-socket listener (ai-mcp-proxy's internal re-entry)
	// reach the route.
	MCPToolsetGateAccess = `local addr = ngx.var.server_addr or ""
if addr:sub(1, 5) ~= "unix:" then
  return ngx.exit(ngx.HTTP_NOT_FOUND)
end
`
)

// MCPToolsetGateConfig returns a fresh pre-function config for the gate.
func MCPToolsetGateConfig() map[string]any {
	return map[string]any{"access": []any{MCPToolsetGateAccess}}
}

// IsMCPToolsetGate reports whether a plugin with the given name and tags is
// the gate the forward direction emits.
func IsMCPToolsetGate(name string, tags []string) bool {
	return name == MCPToolsetGatePlugin && slices.Contains(tags, MCPToolsetGateTag)
}
