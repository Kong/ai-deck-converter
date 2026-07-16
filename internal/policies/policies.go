// Package policies records the plugin-config schema differences between
// AI Gateway 2.0 and API Gateway 3.15, and helpers to apply them when lifting a
// decK config back into the AI Gateway model.
//
// The mapping was derived from the Kong EE repository by comparing
// kong/plugins/<name>/schema.lua (and the shared kong/llm/schemas/* modules) on
// two branches:
//
//	AI Gateway 2.0  -> branch aigw-master     (tip 9c0801c043)
//	API Gateway 3.15 -> branch origin/master  (tip cdf69eb868)
//
// It can be regenerated/audited by reading each schema on both refs without a
// checkout, e.g.:
//
//	git -C <kong-ee> show aigw-master:kong/plugins/ai-semantic-prompt-guard/schema.lua
//	git -C <kong-ee> show origin/master:kong/plugins/ai-semantic-prompt-guard/schema.lua
//
// The reverter (package revert) uses RemovedIn20 / RemovedEnumValue to drop
// fields and enum values that exist in a 3.15 config but are rejected by the
// AI Gateway 2.0 schema, so migrated policies validate cleanly.
package policies

// SchemaDiff describes how one plugin's config schema differs between
// API Gateway 3.15 and AI Gateway 2.0. Dot-path segments address nested fields;
// the segment "[]" denotes "every element of this array" (e.g.
// "targets[].model.options.upstream_path").
type SchemaDiff struct {
	// RemovedIn20 are config dot-paths present in 3.15 but absent in 2.0. A
	// 3.15 config carrying these would be rejected by the 2.0 schema, so the
	// reverter deletes them.
	RemovedIn20 []string
	// RemovedEnumValue maps a field dot-path to enum values accepted in 3.15
	// but no longer valid in 2.0.
	RemovedEnumValue map[string][]string
	// AddedIn20 are 2.0-only config paths. Kept for reference/documentation;
	// their absence in a 3.15 config is harmless.
	AddedIn20 []string
	// ChangedDefault maps a field dot-path to its {3.15, 2.0} default values.
	// Kept for reference; the 3.15 value remains valid in 2.0, so the reverter
	// does not rewrite it.
	ChangedDefault map[string][2]any
}

// Diffs holds the schema diff per plugin name. Plugins whose config is identical
// across the two versions are omitted.
var Diffs = map[string]SchemaDiff{
	"ai-proxy-advanced": {
		RemovedIn20:      []string{"targets[].model.options.upstream_path"},
		RemovedEnumValue: map[string][]string{"targets[].route_type": {"preserve"}},
		AddedIn20: []string{
			"proxy_config", "pricing",
			"targets[].model.options.cohere.api_version",
			"targets[].model.options.kimi",
			"targets[].model.options.sagemaker",
			"embeddings.model.options.databricks",
		},
		ChangedDefault: map[string][2]any{
			"max_request_body_size": {1048576, 8388608},
		},
	},
	"ai-mcp-proxy": {
		ChangedDefault: map[string][2]any{"max_request_body_size": {1048576, 8388608}},
	},
	"ai-a2a-proxy": {
		ChangedDefault: map[string][2]any{"max_request_body_size": {1048576, 8388608}},
	},
	"ai-semantic-prompt-guard": {
		RemovedIn20: []string{"llm_format", "max_request_body_size", "rules.max_request_body_size"},
		AddedIn20:   []string{"proxy_config", "continue_on_detection", "rejection_mode"},
	},
	"ai-prompt-guard": {
		AddedIn20:      []string{"continue_on_detection", "rejection_mode"},
		ChangedDefault: map[string][2]any{"max_request_body_size": {1048576, 8388608}},
	},
	"ai-prompt-decorator": {
		ChangedDefault: map[string][2]any{"max_request_body_size": {1048576, 8388608}},
	},
	"ai-sanitizer": {
		AddedIn20: []string{"proxy_config", "continue_on_detection", "rejection_mode"},
	},
	"ai-prompt-compressor": {
		AddedIn20: []string{"proxy_config"},
	},
	"ai-aws-guardrails": {
		AddedIn20: []string{"proxy_config", "continue_on_detection", "rejection_mode"},
	},
	"ai-azure-content-safety": {
		AddedIn20: []string{"proxy_config", "continue_on_detection", "rejection_mode"},
	},
	"ai-gcp-model-armor": {
		AddedIn20: []string{"proxy_config", "continue_on_detection", "rejection_mode"},
	},
	"ai-custom-guardrail": {
		AddedIn20: []string{"proxy_config", "continue_on_detection", "rejection_mode"},
	},
	"ai-rag-injector": {
		AddedIn20: []string{"proxy_config"},
	},
	"ai-semantic-cache": {
		AddedIn20: []string{"proxy_config"},
	},
	"ai-rate-limiting-advanced": {
		// llm_providers (and its nested fields) plus llm_format were removed in
		// 2.0 in favor of the required `policies` array.
		RemovedIn20: []string{"llm_providers", "llm_format"},
	},
	"rate-limiting-advanced": {
		RemovedIn20: []string{"counter_key"},
	},
	"http-log": {
		RemovedIn20: []string{"client_certificate"},
	},
	"opentelemetry": {
		RemovedIn20: []string{"metrics.enable_principal_attribute"},
	},
}

// SanitizeConfig returns a deep copy of cfg for the named plugin with the fields
// and enum values that AI Gateway 2.0 no longer accepts removed. The input map
// is never mutated. cfg is returned as-is (still copied) when the plugin has no
// recorded diff.
func SanitizeConfig(pluginName string, cfg map[string]any) map[string]any {
	clone := deepCopyMap(cfg)
	diff, ok := Diffs[pluginName]
	if !ok {
		return clone
	}
	for _, path := range diff.RemovedIn20 {
		deletePath(clone, splitPath(path))
	}
	for path, values := range diff.RemovedEnumValue {
		removeEnumValues(clone, splitPath(path), values)
	}
	return clone
}
