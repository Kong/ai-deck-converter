package aimap

import "github.com/Kong/ai-deck-converter/internal/aigw"

// mcpRateLimitingModes are the MCP Server types that accept rate_limiting in
// the AI Gateway API.
var mcpRateLimitingModes = map[string]bool{
	"conversion-listener":  true,
	"passthrough-listener": true,
}

// MCPRateLimitingAllowed reports whether an MCP Server of type mode accepts
// rate_limiting.
func MCPRateLimitingAllowed(mode string) bool {
	return mcpRateLimitingModes[mode]
}

// MCPRateLimitingToPlugin changes rl to the ai-mcp-proxy plugin's
// rate_limiting record. It returns nil when rl is nil.
func MCPRateLimitingToPlugin(rl *aigw.MCPRateLimiting) map[string]any {
	if rl == nil {
		return nil
	}
	limits := make([]any, 0, len(rl.Limits))
	for _, l := range rl.Limits {
		entry := map[string]any{"limit": l.Limit, "window_size": l.WindowSize}
		if l.Method != nil {
			entry["method"] = *l.Method
		}
		if l.Tool != nil {
			entry["tool"] = *l.Tool
		}
		limits = append(limits, entry)
	}
	return map[string]any{"limits": limits}
}

// MCPRateLimitingFromPlugin changes the ai-mcp-proxy plugin's rate_limiting
// record back to an MCPRateLimiting. It returns nil when the block has no
// limits.
func MCPRateLimitingFromPlugin(block map[string]any) *aigw.MCPRateLimiting {
	rl := &aigw.MCPRateLimiting{}
	for _, raw := range getAnySlice(block, "limits") {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var l aigw.MCPRateLimit
		if v, ok := entry["method"].(string); ok {
			l.Method = &v
		}
		if v, ok := entry["tool"].(string); ok {
			l.Tool = &v
		}
		if v := getAnyIntPtr(entry, "limit"); v != nil {
			l.Limit = *v
		}
		if v := getAnyIntPtr(entry, "window_size"); v != nil {
			l.WindowSize = *v
		}
		rl.Limits = append(rl.Limits, l)
	}
	if len(rl.Limits) == 0 {
		return nil
	}
	return rl
}
