package aimap

import (
	"sort"
	"strings"
)

// MCPToolRouteTag marks a Kong Route that the forward converter synthesizes
// as a companion to an MCP conversion tool with no per-tool host override
// (see convert/mcp.go). It is not a label-derived tag (MCP tools have no
// labels field in the AI Gateway schema) but a converter-internal provenance
// marker: it lets the reverter (revert/service.go) recognize the route as
// fully recoverable from the co-located ai-mcp-proxy plugin's own tools[]
// config, rather than an unrelated hand-authored route it must preserve.
const MCPToolRouteTag = "ai-deck-converter:mcp-tool-route"

// LabelsToTags flattens an AI Gateway label map into a deterministic, sorted
// list of "key:value" tag strings (decK has no native label field on most
// entities). The optional prefix namespaces the tags, e.g. "aigw/env:prod".
func LabelsToTags(labels map[string]string, prefix string) []string {
	if len(labels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tags := make([]string, 0, len(labels))
	for _, k := range keys {
		tags = append(tags, prefix+k+":"+labels[k])
	}
	return tags
}

// TagsToLabels reverses LabelsToTags: tags of the form "<prefix><key>:<value>"
// become label entries. Tags that lack the prefix or a ":" separator do not
// look like converted labels and are returned in rest, in input order.
func TagsToLabels(tags []string, prefix string) (labels map[string]string, rest []string) {
	for _, tag := range tags {
		body, ok := strings.CutPrefix(tag, prefix)
		if !ok {
			rest = append(rest, tag)
			continue
		}
		key, value, ok := strings.Cut(body, ":")
		if !ok || key == "" {
			rest = append(rest, tag)
			continue
		}
		if labels == nil {
			labels = map[string]string{}
		}
		labels[key] = value
	}
	return labels, rest
}
