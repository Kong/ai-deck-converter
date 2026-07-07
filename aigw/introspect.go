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

// CapabilitiesFor returns the capabilities a model of the given client format may declare when
// served by the given provider type, with "generate" first when present and the rest sorted. The
// provider type matters only for the gemini format, which Vertex serves with a wider capability
// set than Gemini. An unknown format yields nil.
func CapabilitiesFor(format, providerType string) []string {
	return aimap.CapabilitiesFor(format, providerType)
}
