package convert

import (
	"github.com/gperanich/ai-deck-converter/internal/aigw"
	"github.com/gperanich/ai-deck-converter/internal/aimap"
)

// labelsToTags flattens an AI Gateway label map into a deterministic, sorted
// list of "key:value" tag strings, namespaced by Options.LabelTagPrefix.
func (c *Converter) labelsToTags(labels aigw.Labels) []string {
	return aimap.LabelsToTags(labels, c.opts.LabelTagPrefix)
}
