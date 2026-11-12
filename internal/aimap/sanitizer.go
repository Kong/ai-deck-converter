package aimap

import (
	"maps"
	"slices"
)

const (
	anonymizeAll                = "all"
	anonymizeCredentials        = "credentials"
	anonymizeAllAndCredentials  = "all_and_credentials" //nolint:gosec // an anonymize type, not a credential
	sanitizerAnonymizeConfigKey = "anonymize"
)

// anonymizeTypes returns the string entries of an ai-sanitizer anonymize list.
func anonymizeTypes(cfg map[string]any) []string {
	var types []string
	switch v := cfg[sanitizerAnonymizeConfigKey].(type) {
	case []string:
		types = v
	case []any:
		for _, t := range v {
			if s, ok := t.(string); ok {
				types = append(types, s)
			}
		}
	}
	return types
}

// CollapseSanitizerAnonymize replaces "all" and "credentials" in an
// ai-sanitizer anonymize list with one "all_and_credentials" entry.
// Data planes without FeatureSanitizerAllWithCredentials drop "credentials"
// from a list that also holds "all". It never changes cfg.
func CollapseSanitizerAnonymize(cfg map[string]any) map[string]any {
	types := anonymizeTypes(cfg)
	if !slices.Contains(types, anonymizeAll) || !slices.Contains(types, anonymizeCredentials) {
		return cfg
	}
	collapsed := make([]any, 0, len(types))
	added := false
	for _, t := range types {
		switch t {
		case anonymizeAll, anonymizeCredentials, anonymizeAllAndCredentials:
			if !added {
				collapsed = append(collapsed, anonymizeAllAndCredentials)
				added = true
			}
		default:
			collapsed = append(collapsed, t)
		}
	}
	out := maps.Clone(cfg)
	out[sanitizerAnonymizeConfigKey] = collapsed
	return out
}
