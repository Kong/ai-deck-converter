// Package kongctlconvert adapts the converter's AI Gateway entity model to the
// kongctl declarative AI Gateway file shape.
package kongctlconvert

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/Kong/ai-deck-converter/convert"
	"github.com/Kong/ai-deck-converter/revert"
	"gopkg.in/yaml.v3"
)

const (
	FormatDeck    = "deck"
	FormatKongctl = "kongctl"

	topLevelGateways = "ai_gateways"
)

var childKeys = map[string]string{
	"providers":       "ai_gateway_providers",
	"policies":        "ai_gateway_policies",
	"agents":          "ai_gateway_agents",
	"consumers":       "ai_gateway_consumers",
	"consumer_groups": "ai_gateway_consumer_groups",
	"models":          "ai_gateway_models",
	"mcp_servers":     "ai_gateway_mcp_servers",
	"vaults":          "ai_gateway_vaults",
}

// Options controls kongctl-friendly conversion.
type Options struct {
	From               string
	To                 string
	GatewayName        string
	GatewayDisplayName string
	Strict             bool
	LabelTagPrefix     string
}

// Convert transforms between Kong Gateway decK and kongctl AI Gateway
// declarative YAML.
func Convert(src []byte, opts Options) ([]byte, []string, error) {
	opts.From = strings.TrimSpace(opts.From)
	opts.To = strings.TrimSpace(opts.To)
	opts.GatewayName = strings.TrimSpace(opts.GatewayName)
	opts.GatewayDisplayName = strings.TrimSpace(opts.GatewayDisplayName)
	opts.LabelTagPrefix = strings.TrimSpace(opts.LabelTagPrefix)

	if opts.From == "" {
		return nil, nil, fmt.Errorf("--from is required")
	}
	if opts.To == "" {
		return nil, nil, fmt.Errorf("--to is required")
	}
	if opts.GatewayName == "" {
		return nil, nil, fmt.Errorf("--gateway-name is required")
	}

	switch {
	case opts.From == FormatDeck && opts.To == FormatKongctl:
		return deckToKongctl(src, opts)
	case opts.From == FormatKongctl && opts.To == FormatDeck:
		return kongctlToDeck(src, opts)
	case opts.From == opts.To:
		return nil, nil, fmt.Errorf("--from and --to must be different")
	default:
		return nil, nil, fmt.Errorf("unsupported conversion --from %q --to %q", opts.From, opts.To)
	}
}

func deckToKongctl(src []byte, opts Options) ([]byte, []string, error) {
	native, warnings, err := revert.Revert(src, revert.Options{
		Strict:         opts.Strict,
		LabelTagPrefix: opts.LabelTagPrefix,
	})
	if err != nil {
		return nil, warnings, err
	}

	var doc map[string]any
	if err := yaml.Unmarshal(native, &doc); err != nil {
		return nil, warnings, fmt.Errorf("parse reverted AI Gateway document: %w", err)
	}

	gateway := map[string]any{
		"ref":          opts.GatewayName,
		"name":         opts.GatewayName,
		"display_name": opts.GatewayName,
	}
	if opts.GatewayDisplayName != "" {
		gateway["display_name"] = opts.GatewayDisplayName
	}

	for key := range childKeys {
		items := mapsFromAny(doc[key])
		if len(items) == 0 {
			continue
		}
		for i := range items {
			adaptNativeChildToKongctl(key, items[i], &warnings)
		}
		gateway[key] = items
	}

	if opts.Strict && len(warnings) > 0 {
		return nil, warnings, fmt.Errorf("conversion produced warnings in strict mode")
	}

	return marshalYAML(map[string]any{topLevelGateways: []map[string]any{gateway}}, warnings)
}

func kongctlToDeck(src []byte, opts Options) ([]byte, []string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse kongctl AI Gateway document: %w", err)
	}

	gateway, gatewayRef, err := selectGateway(doc, opts.GatewayName)
	if err != nil {
		return nil, nil, err
	}

	native := map[string]any{}
	for nativeKey, rootKey := range childKeys {
		items := append([]map[string]any{}, mapsFromAny(gateway[nativeKey])...)
		items = append(items, rootChildrenForGateway(doc[rootKey], gatewayRef, opts.GatewayName)...)
		if len(items) == 0 {
			continue
		}
		for i := range items {
			adaptKongctlChildToNative(nativeKey, items[i])
		}
		native[nativeKey] = items
	}
	attachRootCredentials(doc, native, gatewayRef, opts.GatewayName)

	nativeYAML, err := marshalYAMLOnly(native)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal converter AI Gateway document: %w", err)
	}

	return convert.Convert(nativeYAML, convert.Options{
		Strict:         opts.Strict,
		LabelTagPrefix: opts.LabelTagPrefix,
		OutputMode:     "deck",
	})
}

func selectGateway(doc map[string]any, gatewayName string) (map[string]any, string, error) {
	var matches []map[string]any
	for _, gateway := range mapsFromAny(doc[topLevelGateways]) {
		if stringField(gateway, "name") == gatewayName {
			matches = append(matches, gateway)
		}
	}
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("ai_gateway with name %q not found", gatewayName)
	}
	if len(matches) > 1 {
		return nil, "", fmt.Errorf("ai_gateway name %q matched %d gateways", gatewayName, len(matches))
	}
	ref := stringField(matches[0], "ref")
	if ref == "" {
		ref = gatewayName
	}
	return matches[0], ref, nil
}

func rootChildrenForGateway(raw any, gatewayRef, gatewayName string) []map[string]any {
	children := mapsFromAny(raw)
	out := make([]map[string]any, 0, len(children))
	for _, child := range children {
		parent := stringField(child, "ai_gateway")
		if parent == gatewayRef || parent == gatewayName {
			out = append(out, child)
		}
	}
	return out
}

func attachRootCredentials(doc, native map[string]any, gatewayRef, gatewayName string) {
	rawCreds := mapsFromAny(doc["ai_gateway_consumer_credentials"])
	if len(rawCreds) == 0 {
		return
	}
	consumers := mapsFromAny(native["consumers"])
	if len(consumers) == 0 {
		return
	}

	selected := map[string]map[string]any{}
	for _, consumer := range consumers {
		for _, key := range []string{"ref", "name"} {
			if value := stringField(consumer, key); value != "" {
				selected[value] = consumer
			}
		}
	}
	for _, cred := range rawCreds {
		parent := stringField(cred, "ai_gateway_consumer")
		consumer := selected[parent]
		if consumer == nil {
			continue
		}
		if parentGateway := stringField(consumer, "ai_gateway"); parentGateway != "" &&
			parentGateway != gatewayRef && parentGateway != gatewayName {
			continue
		}
		delete(cred, "ai_gateway_consumer")
		adaptKongctlCredentialToNative(cred)
		consumer["credentials"] = append(mapsFromAny(consumer["credentials"]), cred)
	}
}

func adaptNativeChildToKongctl(kind string, item map[string]any, warnings *[]string) {
	ref := stringField(item, "id")
	if ref == "" {
		ref = stringField(item, "name")
	}
	if ref != "" {
		item["ref"] = ref
	}
	delete(item, "id")

	if stringField(item, "display_name") == "" {
		if name := stringField(item, "name"); name != "" {
			item["display_name"] = name
		}
	}

	switch kind {
	case "models":
		if targets, ok := item["targets"]; ok {
			item["target_models"] = targets
			delete(item, "targets")
		}
	case "consumers":
		if groups := stringsFromAny(item["consumer_groups"]); len(groups) > 0 {
			*warnings = append(*warnings,
				fmt.Sprintf("consumer %q: consumer_groups is not supported by kongctl AI Gateway consumers; dropped", stringField(item, "name")))
		}
		delete(item, "consumer_groups")
		credentials := mapsFromAny(item["credentials"])
		for i := range credentials {
			adaptNativeCredentialToKongctl(stringField(item, "name"), i, credentials[i], warnings)
		}
		if len(credentials) > 0 {
			item["credentials"] = credentials
		}
	case "mcp_servers":
		if upstreamURL := stringField(item, "upstream_url"); upstreamURL != "" {
			config := mapFromAny(item["config"])
			config["url"] = upstreamURL
			item["config"] = config
		}
		delete(item, "upstream_url")
	}
}

func adaptNativeCredentialToKongctl(consumerName string, index int, item map[string]any, warnings *[]string) {
	ref := stringField(item, "id")
	name := stringField(item, "name")
	if name == "" {
		if consumerName == "" {
			name = fmt.Sprintf("credential-%d", index+1)
		} else if index == 0 {
			name = consumerName + "-credential"
		} else {
			name = fmt.Sprintf("%s-credential-%d", consumerName, index+1)
		}
		item["name"] = name
	}
	if ref == "" {
		ref = name
	}
	item["ref"] = ref
	delete(item, "id")
	if stringField(item, "display_name") == "" {
		item["display_name"] = name
	}
	if item["type"] == nil || item["type"] == "" {
		item["type"] = "api-key"
	}
	if stringField(item, "api_key") != "" {
		*warnings = append(*warnings,
			fmt.Sprintf("consumer credential %q: api_key is write-only in kongctl AI Gateway credentials; dropped", name))
	}
	delete(item, "api_key")
}

func adaptKongctlChildToNative(kind string, item map[string]any) {
	adaptKongctlRefToNativeID(item)
	delete(item, "kongctl")
	delete(item, "managed_by")
	switch kind {
	case "models":
		if targets, ok := item["target_models"]; ok {
			item["targets"] = targets
			delete(item, "target_models")
		}
	case "consumers":
		credentials := mapsFromAny(item["credentials"])
		for i := range credentials {
			adaptKongctlCredentialToNative(credentials[i])
		}
		if len(credentials) > 0 {
			item["credentials"] = credentials
		}
	case "mcp_servers":
		config := mapFromAny(item["config"])
		if url := stringField(config, "url"); url != "" {
			item["upstream_url"] = url
		}
	}
}

func adaptKongctlCredentialToNative(item map[string]any) {
	adaptKongctlRefToNativeID(item)
	delete(item, "ai_gateway_consumer")
	delete(item, "kongctl")
	delete(item, "managed_by")
}

func adaptKongctlRefToNativeID(item map[string]any) {
	if stringField(item, "id") == "" {
		if ref := stringField(item, "ref"); ref != "" {
			item["id"] = ref
		}
	}
	delete(item, "ref")
}

func mapsFromAny(raw any) []map[string]any {
	if typed, ok := raw.([]map[string]any); ok {
		return typed
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m := mapFromAny(item); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func mapFromAny(raw any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	if m, ok := raw.(map[any]any); ok {
		out := make(map[string]any, len(m))
		for key, value := range m {
			if keyString, ok := key.(string); ok {
				out[keyString] = value
			}
		}
		return out
	}
	return nil
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
}

func stringsFromAny(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func marshalYAML(v any, warnings []string) ([]byte, []string, error) {
	data, err := marshalYAMLOnly(v)
	return data, warnings, err
}

func marshalYAMLOnly(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
