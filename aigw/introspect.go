package aigw

import "github.com/Kong/ai-deck-converter/internal/aimap"

// introspect.go exposes the converter's endpoint knowledge (which client formats exist and which
// capabilities each supports) so UIs can offer only valid choices instead of maintaining a
// drifting copy. The format/section handling lives in aimap, next to the endpoint table and
// SectionFor.

// Formats returns the client-facing wire formats the converter knows, sorted.
func Formats() []string {
	return aimap.Formats()
}

// CapabilitiesFor returns the capabilities a model of the given client format may declare when
// served by the given provider type, with "generate" first when present and the rest sorted. The
// provider type matters for passthrough-only capabilities (skills), which need a provider
// rendering the model's own format. An unknown format yields nil.
func CapabilitiesFor(format, providerType string) []string {
	return aimap.CapabilitiesFor(format, providerType)
}
