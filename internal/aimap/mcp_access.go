package aimap

// mcpAccessModes are the MCP Server types that accept access.acls and
// access.auth_strategies. The other types front no client-facing listener.
var mcpAccessModes = map[string]bool{
	"listener":             true,
	"conversion-listener":  true,
	"passthrough-listener": true,
}

// MCPAccessAllowed reports whether an MCP Server of type mode accepts server
// access control.
func MCPAccessAllowed(mode string) bool {
	return mcpAccessModes[mode]
}
