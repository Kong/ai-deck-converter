package convert

import (
	"sort"

	"github.com/gperanich/ai-deck-converter/internal/aigw"
)

// labelsToTags flattens an AI Gateway label map into a deterministic, sorted
// list of "key:value" tag strings (decK has no native label field on most
// entities). The optional prefix namespaces the tags, e.g. "aigw/env:prod".
func (c *Converter) labelsToTags(labels aigw.Labels) []string {
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
		tags = append(tags, c.opts.LabelTagPrefix+k+":"+labels[k])
	}
	return tags
}
