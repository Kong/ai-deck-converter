package policies

import (
	"reflect"
	"testing"
)

func TestDiffsWellFormed(t *testing.T) {
	for name, diff := range Diffs {
		if name == "" {
			t.Errorf("empty plugin name in Diffs")
		}
		for _, p := range diff.RemovedIn20 {
			if p == "" {
				t.Errorf("%s: empty path in RemovedIn20", name)
			}
		}
		for p, vals := range diff.RemovedEnumValue {
			if p == "" || len(vals) == 0 {
				t.Errorf("%s: bad RemovedEnumValue entry %q=%v", name, p, vals)
			}
		}
	}
}

func TestSanitizeConfigUnknownPluginCopies(t *testing.T) {
	in := map[string]any{"a": 1}
	out := SanitizeConfig("no-such-plugin", in)
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("expected identical copy, got %v", out)
	}
	out["a"] = 2
	if in["a"] != 1 {
		t.Fatalf("input mutated: %v", in)
	}
}

func TestSanitizeConfigRemovesFlatAndNested(t *testing.T) {
	in := map[string]any{
		"llm_format":            "openai",
		"max_request_body_size": 1048576,
		"rules":                 map[string]any{"max_request_body_size": 100, "deny_prompts": []any{"x"}},
		"search":                map[string]any{"threshold": 0.5},
	}
	out := SanitizeConfig("ai-semantic-prompt-guard", in)

	if _, ok := out["llm_format"]; ok {
		t.Error("llm_format not removed")
	}
	if _, ok := out["max_request_body_size"]; ok {
		t.Error("top-level max_request_body_size not removed")
	}
	rules := out["rules"].(map[string]any)
	if _, ok := rules["max_request_body_size"]; ok {
		t.Error("rules.max_request_body_size not removed")
	}
	if _, ok := rules["deny_prompts"]; !ok {
		t.Error("rules.deny_prompts should be kept")
	}
	// input untouched
	if _, ok := in["llm_format"]; !ok {
		t.Error("input map was mutated")
	}
}

func TestSanitizeConfigArrayWildcardAndEnum(t *testing.T) {
	in := map[string]any{
		"targets": []any{
			map[string]any{
				"route_type": "preserve",
				"model": map[string]any{
					"options": map[string]any{"upstream_path": "/v1", "max_tokens": 10},
				},
			},
			map[string]any{
				"route_type": "llm/v1/chat",
				"model": map[string]any{
					"options": map[string]any{"upstream_path": "/v2"},
				},
			},
		},
	}
	out := SanitizeConfig("ai-proxy-advanced", in)
	targets := out["targets"].([]any)

	t0 := targets[0].(map[string]any)
	if _, ok := t0["route_type"]; ok {
		t.Error("route_type=preserve should be dropped")
	}
	opts0 := t0["model"].(map[string]any)["options"].(map[string]any)
	if _, ok := opts0["upstream_path"]; ok {
		t.Error("upstream_path not removed on target 0")
	}
	if _, ok := opts0["max_tokens"]; !ok {
		t.Error("max_tokens should be kept")
	}

	t1 := targets[1].(map[string]any)
	if t1["route_type"] != "llm/v1/chat" {
		t.Error("non-preserve route_type should be kept")
	}
	opts1 := t1["model"].(map[string]any)["options"].(map[string]any)
	if _, ok := opts1["upstream_path"]; ok {
		t.Error("upstream_path not removed on target 1")
	}
}

func TestSanitizeConfigRemovesRateLimitingFields(t *testing.T) {
	in := map[string]any{
		"llm_providers": nil,
		"llm_format":    "openai",
		"policies":      []any{map[string]any{"limits": []any{}}},
	}
	out := SanitizeConfig("ai-rate-limiting-advanced", in)
	if _, ok := out["llm_providers"]; ok {
		t.Error("llm_providers not removed")
	}
	if _, ok := out["llm_format"]; ok {
		t.Error("llm_format not removed")
	}
	if _, ok := out["policies"]; !ok {
		t.Error("policies should be kept")
	}
}
