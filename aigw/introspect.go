package aigw

import "github.com/Kong/ai-deck-converter/internal/aimap"

// introspect.go exposes the converter's endpoint knowledge (which client formats exist and which
// capabilities each supports) so UIs can offer only valid choices instead of maintaining a
// drifting copy. The format/section handling lives in aimap, next to the endpoint table and
// SectionFor: provider-rendering sections such as Vertex are folded into their base format
// (gemini) rather than surfaced as formats of their own.

// Formats returns the client-facing wire formats the converter knows, sorted.
func Formats() []string {
	return aimap.Formats()
}

// CapabilitiesFor returns the capabilities a model of the given client format may declare, with
// "generate" first when present and the rest sorted. An unknown format yields nil.
func CapabilitiesFor(format string) []string {
	return aimap.CapabilitiesFor(format)
}
