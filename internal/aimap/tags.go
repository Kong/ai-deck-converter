package aimap

import (
	"sort"
	"strings"
)

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

// managerNameTagKey is a Konnect-internal marker tag ("ai-manager-name: <cp>")
// that is not an AI Gateway label; it is dropped entirely on reversal.
const managerNameTagKey = "ai-manager-name"

// TagsToLabels reverses LabelsToTags: tags of the form "<prefix><key>:<value>"
// become label entries. Tags that lack the prefix or a ":" separator do not
// look like converted labels and are returned in rest, in input order. The
// Konnect-internal "ai-manager-name" tag is dropped (neither a label nor rest).
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
		if strings.TrimSpace(key) == managerNameTagKey {
			continue
		}
		if labels == nil {
			labels = map[string]string{}
		}
		labels[key] = value
	}
	return labels, rest
}
