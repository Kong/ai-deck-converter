package revert

import (
	"fmt"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// Accessors for plugin config maps decoded from YAML (values are untyped).

func getStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func getBool(m map[string]any, key string) *bool {
	if b, ok := m[key].(bool); ok {
		return &b
	}
	return nil
}

func getInt(m map[string]any, key string) *int {
	switch v := m[key].(type) {
	case int:
		return &v
	case int64:
		i := int(v)
		return &i
	case uint64:
		i := int(v) //nolint:gosec
		return &i
	case float64:
		i := int(v)
		return &i
	}
	return nil
}

func getMap(m map[string]any, key string) map[string]any {
	mm, _ := m[key].(map[string]any)
	return mm
}

func getSlice(m map[string]any, key string) []any {
	s, _ := m[key].([]any)
	return s
}

// toStrings converts a []any of strings into []string, skipping non-strings.
func toStrings(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// aclsFromBlock reads an {allow, deny} config map into an aigw.ACLs.
func aclsFromBlock(block map[string]any) aigw.ACLs {
	return aigw.ACLs{
		Allow: toStrings(block["allow"]),
		Deny:  toStrings(block["deny"]),
	}
}

// loggingFromBlock reverses the {log_statistics, log_payloads, log_audits,
// max_payload_size} block used by the ai-a2a-proxy and ai-mcp-proxy plugins.
func loggingFromBlock(block map[string]any) *aigw.Logging {
	if len(block) == 0 {
		return nil
	}
	l := &aigw.Logging{
		Statistics:     getBool(block, "log_statistics"),
		Payloads:       getBool(block, "log_payloads"),
		Audits:         getBool(block, "log_audits"),
		MaxPayloadSize: getInt(block, "max_payload_size"),
	}
	if l.Statistics == nil && l.Payloads == nil && l.Audits == nil && l.MaxPayloadSize == nil {
		return nil
	}
	return l
}

// loggingFromBlockWithDefaults reverses a logging block and fills in the same
// defaults the forward converter applies when statistics/payloads (and where
// applicable, audits/max_payload_size) are absent from decK. This mirrors
// convert's withLoggingDefaults behavior on the reverse path.
func loggingFromBlockWithDefaults(block map[string]any, defaultAudits, defaultMaxPayloadSize bool) *aigw.Logging {
	l := loggingFromBlock(block)
	if l == nil {
		l = &aigw.Logging{}
	}
	if l.Statistics == nil {
		l.Statistics = boolPtr(aimap.DefaultLogStatistics)
	}
	if l.Payloads == nil {
		l.Payloads = boolPtr(aimap.DefaultLogPayloads)
	}
	if defaultAudits && l.Audits == nil {
		l.Audits = boolPtr(aimap.DefaultLogAudits)
	}
	if defaultMaxPayloadSize && l.MaxPayloadSize == nil {
		l.MaxPayloadSize = intPtr(aimap.DefaultMaxPayloadSize)
	}
	return l
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int   { return &i }

// routeConfig lifts a Kong route back into an AI Gateway route config. The
// route name is omitted when it matches the forward converter's default
// ("<entity>-route"), so defaulted names do not round-trip as explicit config.
func routeConfig(rt *kong.Route, entityName string) aigw.RouteConfig {
	rc := aigw.RouteConfig{
		Paths:        rt.Paths,
		Hosts:        rt.Hosts,
		Methods:      rt.Methods,
		Protocols:    rt.Protocols,
		Headers:      rt.Headers,
		StripPath:    rt.StripPath,
		PreserveHost: rt.PreserveHost,
		Tags:         rt.Tags,
	}
	if rt.Name != entityName+"-route" {
		rc.Name = rt.Name
	}
	return rc
}

// serviceURL reconstructs an upstream URL from a Kong service, preferring the
// url shorthand over host/port/protocol/path fields.
func serviceURL(svc *kong.Service) string {
	if svc.URL != "" {
		return svc.URL
	}
	if svc.Host == "" {
		return ""
	}
	scheme := svc.Protocol
	if scheme == "" {
		scheme = "http"
	}
	u := scheme + "://" + svc.Host
	if svc.Port != nil {
		u += fmt.Sprintf(":%d", *svc.Port)
	}
	return u + svc.Path
}
