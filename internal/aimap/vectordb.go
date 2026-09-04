package aimap

import "strings"

// vectordb.go maps the AI Gateway model's `balancer.vectordb` block onto the
// ai-proxy-advanced `vectordb` config and back. The two shapes carry the same
// settings differently: the plugin keeps only the strategy-agnostic fields at
// the top of the block and nests the strategy's own settings under a sub-block
// named by `strategy` (`redis`/`pgvector`, the only records the vectordb schema
// declares), spelling grouped settings as flat prefixed keys. The AI Gateway
// model authors the strategy's settings flat alongside the common ones and
// keeps each group as a nested object.

// vectorDBCommonKeys are the vectordb fields the plugin keeps at the top of the
// block; every other field belongs to the strategy sub-block.
var vectorDBCommonKeys = map[string]bool{
	"strategy":        true,
	"dimensions":      true,
	"distance_metric": true,
	"threshold":       true,
}

// vectorDBGroups are, per strategy, the AI Gateway nested objects whose fields
// the plugin schema spells as flat "<group>_<field>" keys — pgvector's TLS
// settings and redis' cluster, connection-pool, and sentinel settings. A group
// field named `enabled` maps onto the bare group name, the flag the qualifying
// keys hang off (pgvector `ssl.enabled` -> `ssl`, `ssl.verify` -> `ssl_verify`).
// Groups the plugin itself keeps nested (redis' `cloud_authentication`) are
// absent here and pass through untouched.
var vectorDBGroups = map[string][]string{
	"pgvector": {"ssl"},
	"redis":    {"cluster", "keepalive", "sentinel"},
}

// groupEnabledField is the AI Gateway group field carried by the bare group key.
const groupEnabledField = "enabled"

// VectorDBToPlugin lowers an AI Gateway vectordb block into the plugin's shape:
// strategy-specific fields authored flat move under the sub-block named by
// `strategy`, merging with a sub-block the input already nests, and each grouped
// object flattens into its prefixed keys. A block with an absent or unrecognized
// strategy is passed through unchanged (there is no sub-block name to nest
// under), as is a non-map value. Already-lowered input converts to itself, so
// lowering a reverted config is a no-op.
func VectorDBToPlugin(raw any) any {
	src, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	strategy, _ := src["strategy"].(string)
	if !isVectorDBStrategy(strategy) {
		return raw
	}

	out := make(map[string]any, len(src))
	nested := map[string]any{}
	for k, v := range src {
		switch {
		case vectorDBCommonKeys[k]:
			out[k] = v
		case k == strategy:
			// The block already nests its own strategy: merge it so flat and
			// nested authoring can coexist (and so relowering is idempotent).
			if sub, ok := v.(map[string]any); ok {
				for sk, sv := range sub {
					nested[sk] = sv
				}
				continue
			}
			out[k] = v
		case isVectorDBStrategy(k):
			// Another strategy's sub-block: not this strategy's setting, so
			// leave it where it was authored.
			out[k] = v
		default:
			nested[k] = v
		}
	}
	flattenGroups(nested, vectorDBGroups[strategy])
	if len(nested) > 0 {
		out[strategy] = nested
	}
	return out
}

// VectorDBFromPlugin lifts an ai-proxy-advanced vectordb block back into the AI
// Gateway shape, inverting VectorDBToPlugin: the strategy sub-block's fields
// move up alongside the common ones and its prefixed keys regroup into nested
// objects. A block that nests no sub-block for its strategy is already in the
// AI Gateway shape and is returned unchanged.
func VectorDBFromPlugin(raw any) any {
	src, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	strategy, _ := src["strategy"].(string)
	nested, ok := src[strategy].(map[string]any)
	if !ok {
		return raw
	}

	out := make(map[string]any, len(src)+len(nested))
	for k, v := range src {
		if k != strategy {
			out[k] = v
		}
	}
	for k, v := range nested {
		out[k] = v
	}
	groupFlatKeys(out, vectorDBGroups[strategy])
	return out
}

// isVectorDBStrategy reports whether name is a strategy the plugin nests a
// sub-block for; vectorDBGroups is keyed by exactly those strategies.
func isVectorDBStrategy(name string) bool {
	_, ok := vectorDBGroups[name]
	return ok
}

// flattenGroups rewrites each named AI Gateway object into the plugin's flat
// "<group>_<field>" keys. A group that is absent, or already flattened to the
// scalar the bare key carries, is left alone.
func flattenGroups(cfg map[string]any, groups []string) {
	for _, group := range groups {
		fields, ok := cfg[group].(map[string]any)
		if !ok {
			continue
		}
		delete(cfg, group)
		for k, v := range fields {
			if k == groupEnabledField {
				cfg[group] = v
				continue
			}
			cfg[group+"_"+k] = v
		}
	}
}

// groupFlatKeys collects each group's flat "<group>_<field>" keys back into a
// nested AI Gateway object, reversing flattenGroups.
func groupFlatKeys(cfg map[string]any, groups []string) {
	for _, group := range groups {
		fields := map[string]any{}
		prefix := group + "_"
		for k, v := range cfg {
			switch {
			case k == group:
				fields[groupEnabledField] = v
			case strings.HasPrefix(k, prefix):
				fields[strings.TrimPrefix(k, prefix)] = v
			default:
				continue
			}
			delete(cfg, k)
		}
		if len(fields) > 0 {
			cfg[group] = fields
		}
	}
}
