package policies

import "strings"

// splitPath splits a dot-path into segments. The literal segment "[]" denotes
// "every element of the array at this position".
func splitPath(path string) []string {
	return strings.Split(path, ".")
}

// deepCopyMap returns a deep copy of m, recursing into nested map[string]any and
// []any so the original (a shared plugin config) is never mutated.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}

// deletePath removes the value addressed by segs from the (possibly nested)
// container. A segment ending in "[]" descends into that key and fans out over
// every element of the resulting array.
func deletePath(container any, segs []string) {
	if len(segs) == 0 {
		return
	}
	m, ok := container.(map[string]any)
	if !ok {
		return
	}
	name, isArray := arraySeg(segs[0])
	last := len(segs) == 1

	if isArray {
		fanOut(m[name], func(e any) { deletePath(e, segs[1:]) })
		return
	}
	if last {
		delete(m, name)
		return
	}
	deletePath(m[name], segs[1:])
}

// removeEnumValues drops the given values from the string / string-array field
// addressed by segs (a segment ending in "[]" fans out over an array). A scalar
// field whose value is removed is deleted from its parent map.
func removeEnumValues(container any, segs []string, values []string) {
	if len(segs) == 0 {
		return
	}
	m, ok := container.(map[string]any)
	if !ok {
		return
	}
	name, isArray := arraySeg(segs[0])
	last := len(segs) == 1

	if isArray {
		fanOut(m[name], func(e any) { removeEnumValues(e, segs[1:], values) })
		return
	}
	if !last {
		removeEnumValues(m[name], segs[1:], values)
		return
	}

	drop := make(map[string]bool, len(values))
	for _, v := range values {
		drop[v] = true
	}
	switch field := m[name].(type) {
	case string:
		if drop[field] {
			delete(m, name)
		}
	case []any:
		kept := field[:0]
		for _, e := range field {
			if s, ok := e.(string); ok && drop[s] {
				continue
			}
			kept = append(kept, e)
		}
		m[name] = kept
	}
}

// arraySeg splits a "name[]" segment into ("name", true); a plain "name" is
// ("name", false).
func arraySeg(seg string) (string, bool) {
	if strings.HasSuffix(seg, "[]") {
		return strings.TrimSuffix(seg, "[]"), true
	}
	return seg, false
}

// fanOut invokes fn on each element of v when v is a []any.
func fanOut(v any, fn func(any)) {
	arr, ok := v.([]any)
	if !ok {
		return
	}
	for _, e := range arr {
		fn(e)
	}
}
