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

// decodeTaggedValue finds the first tag carrying prefix and returns the value
// after it, along with the remaining tags in input order.
func decodeTaggedValue(tags []string, prefix string) (value string, rest []string) {
	for _, tag := range tags {
		if body, ok := strings.CutPrefix(tag, prefix); ok && value == "" {
			value = body
			continue
		}
		rest = append(rest, tag)
	}
	return value, rest
}

// caCertificateNameTag namespaces the tag used to preserve an AI Gateway
// CACertificate's name across the round trip: Kong's ca_certificates entity
// has no name field, so decK has nowhere else to keep it.
const caCertificateNameTag = "ai-gateway-name:"

// EncodeCACertificateName renders name as a tag Kong can carry.
func EncodeCACertificateName(name string) string {
	return caCertificateNameTag + name
}

// DecodeCACertificateName finds the tag written by EncodeCACertificateName and
// returns its name, along with the remaining tags in input order.
func DecodeCACertificateName(tags []string) (name string, rest []string) {
	return decodeTaggedValue(tags, caCertificateNameTag)
}

// modelAliasGroupTag namespaces the tag marking every ai_models row/plugin a
// single source model's route.model.values fan-out produced: decK has no way
// to express "these N otherwise-identical entities came from one model", so
// this tag carries that fact across the round trip. Without it, revert cannot
// safely tell that shape apart from N independently-authored models that
// happen to be config-identical.
const modelAliasGroupTag = "ai-gateway-model-alias-group:"

// EncodeModelAliasGroup renders sourceModelName as a tag Kong can carry.
func EncodeModelAliasGroup(sourceModelName string) string {
	return modelAliasGroupTag + sourceModelName
}

// DecodeModelAliasGroup finds the tag written by EncodeModelAliasGroup and
// returns the source model name it names (or "" if absent), along with the
// remaining tags in input order.
func DecodeModelAliasGroup(tags []string) (group string, rest []string) {
	return decodeTaggedValue(tags, modelAliasGroupTag)
}
