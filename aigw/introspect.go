package aigw

import (
	"sort"

	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// introspect.go exposes the endpoint table (which client formats exist and which capabilities
// each supports) so UIs can offer only valid choices instead of maintaining a drifting copy.

// Formats returns the client-facing wire formats the converter knows, sorted.
func Formats() []string {
	out := make([]string, 0, len(aimap.EndpointTable))
	for f := range aimap.EndpointTable {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// CapabilitiesFor returns the capabilities a model of the given client format may declare
// (the keys of the endpoint table for that format), with "generate" first when present and the
// rest sorted. An unknown format yields nil.
func CapabilitiesFor(format string) []string {
	caps, ok := aimap.EndpointTable[format]
	if !ok {
		return nil
	}
	rest := make([]string, 0, len(caps))
	hasGenerate := false
	for c := range caps {
		if c == "generate" {
			hasGenerate = true
			continue
		}
		rest = append(rest, c)
	}
	sort.Strings(rest)
	out := make([]string, 0, len(caps))
	if hasGenerate {
		out = append(out, "generate")
	}
	return append(out, rest...)
}
