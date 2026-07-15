package convert

import (
	"sort"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// resolveAuth folds a provider's auth configuration into an ai-proxy-advanced
// target `auth` block. Returns nil when there is nothing to emit.
func resolveAuth(p *aigw.Provider, allowOverride *bool) map[string]any {
	auth := map[string]any{}
	if p != nil {
		a := p.Config.Auth
		if len(a.Headers) > 0 {
			if a.Headers[0].Name != "" {
				auth["header_name"] = a.Headers[0].Name
			}
			if a.Headers[0].Value != "" {
				auth["header_value"] = a.Headers[0].Value
			}
		}
		if len(a.Params) > 0 {
			if a.Params[0].Name != "" {
				auth["param_name"] = a.Params[0].Name
			}
			if a.Params[0].Value != "" {
				auth["param_value"] = a.Params[0].Value
			}
			if a.Params[0].Location != "" {
				auth["param_location"] = a.Params[0].Location
			}
		}
		// aws
		if a.AccessKeyID != "" {
			auth["aws_access_key_id"] = a.AccessKeyID
		}
		if a.SecretAccessKey != "" {
			auth["aws_secret_access_key"] = a.SecretAccessKey
		}
		// azure
		if a.ClientID != "" {
			auth["azure_client_id"] = a.ClientID
		}
		if a.ClientSecret != "" {
			auth["azure_client_secret"] = a.ClientSecret
		}
		if a.TenantID != "" {
			auth["azure_tenant_id"] = a.TenantID
		}
		if a.UseManagedIdentity != nil {
			auth["azure_use_managed_identity"] = *a.UseManagedIdentity
		}
		// gcp
		if a.ServiceAccountJSON != "" {
			auth["gcp_service_account_json"] = a.ServiceAccountJSON
		}
		if a.UseGCPServiceAccount != nil {
			auth["gcp_use_service_account"] = *a.UseGCPServiceAccount
		} else if a.Type == "gcp" || a.ServiceAccountJSON != "" {
			auth["gcp_use_service_account"] = true
		}
		if a.MetadataURL != "" {
			auth["gcp_metadata_url"] = a.MetadataURL
		}
		if a.OAuthTokenURL != "" {
			auth["gcp_oauth_token_url"] = a.OAuthTokenURL
		}
	}
	if allowOverride != nil {
		auth["allow_override"] = *allowOverride
	}
	if len(auth) == 0 {
		return nil
	}
	return auth
}

// resolveEmbeddings lowers a model's embeddings block into the ai-proxy-advanced
// embeddings shape. When `embeddings.provider` names a top-level provider entity,
// its auth is resolved via resolveAuth and merged into `embeddings.auth` (any
// explicitly configured auth keys win), then the provider reference is dropped —
// the ai-proxy-advanced embeddings schema has no top-level provider field. The
// embeddings model's `config` block is lowered regardless of whether a provider
// entity is referenced.
func (c *Converter) resolveEmbeddings(raw any) (any, error) {
	emb, ok := raw.(map[string]any)
	if !ok {
		return raw, nil
	}

	var provider *aigw.Provider
	if provName, _ := emb["provider"].(string); provName != "" {
		delete(emb, "provider")
		provider = c.providers[provName]
		if provider == nil {
			if err := c.warn("model embeddings reference unknown provider %q; auth may be incomplete", provName); err != nil {
				return nil, err
			}
		}
	}

	dropped := lowerEmbeddingsModel(emb, provider)
	if len(dropped) > 0 {
		name := ""
		if m, ok := emb["model"].(map[string]any); ok {
			name, _ = m["name"].(string)
		}
		if err := c.warn(
			"embeddings model %q: dropped option key(s) %s unsupported by the ai-proxy-advanced model.options schema",
			name, strings.Join(dropped, ", ")); err != nil {
			return nil, err
		}
	}

	if provider == nil {
		return emb, nil
	}
	resolved := resolveAuth(provider, nil)
	if resolved == nil {
		return emb, nil
	}
	existing, _ := emb["auth"].(map[string]any)
	if existing == nil {
		emb["auth"] = resolved
		return emb, nil
	}
	for k, v := range resolved {
		if _, set := existing[k]; !set {
			existing[k] = v
		}
	}
	return emb, nil
}

// lowerEmbeddingsModel lowers an embeddings model's `config` block (a
// TargetModelConfig-shaped {type, ...flat options}) into the ai-proxy-advanced
// embeddings schema's model.{provider, options} shape, reusing the same target
// option mapping (mapOptions). Provider-specific keys nest under
// model.options.<provider>; unlike ai-proxy-advanced targets, azure options nest
// under model.options.azure rather than using flat azure_* keys. A model with no
// `config` block is already lowered (e.g. produced by the reverse converter), so
// it is left untouched — keeping the forward conversion idempotent.
func lowerEmbeddingsModel(emb map[string]any, provider *aigw.Provider) []string {
	model, ok := emb["model"].(map[string]any)
	if !ok {
		return nil
	}
	config, ok := model["config"].(map[string]any)
	if !ok {
		return nil
	}
	delete(model, "config")

	providerType, _ := config["type"].(string)
	if providerType == "" && provider != nil {
		providerType = provider.Type
	}
	model["provider"] = providerType

	opts := map[string]any{}
	for k, v := range config {
		if k != "type" {
			opts[k] = v
		}
	}
	name, _ := model["name"].(string)
	options, dropped := mapOptions(opts, providerType, name, provider)
	if providerType == "azure" {
		options = nestAzureEmbeddingsOptions(options)
	}
	if len(options) > 0 {
		model["options"] = options
	}
	return dropped
}

// nestAzureEmbeddingsOptions rewrites the flat azure_* keys mapOptions emits for
// ai-proxy-advanced targets into the nested model.options.azure.<key> shape the
// embeddings schema expects (e.g. azure_instance -> azure.instance).
func nestAzureEmbeddingsOptions(options map[string]any) map[string]any {
	for k, v := range options {
		suffix, ok := strings.CutPrefix(k, "azure_")
		if !ok {
			continue
		}
		azure, _ := options["azure"].(map[string]any)
		if azure == nil {
			azure = map[string]any{}
			options["azure"] = azure
		}
		azure[suffix] = v
		delete(options, k)
	}
	return options
}

// mapOptions translates a target model's option map into an ai-proxy-advanced
// model.options map. It renames/nests provider-specific keys per provider type
// and folds in provider-level fields (azure instance, gemini project id, bedrock
// assume-role auth). Keys not handled specially pass through flat.
func mapOptions(opts map[string]any, providerType, modelName string,
	provider *aigw.Provider,
) (map[string]any, []string) {
	out := map[string]any{}
	nested := map[string]map[string]any{}
	var dropped []string
	addNested := func(prov, key string, val any) {
		if nested[prov] == nil {
			nested[prov] = map[string]any{}
		}
		nested[prov][key] = val
	}
	// mergeNestedBlock accepts a nested provider record written directly in the
	// DP's own shape (e.g. a hand-authored `bedrock: {...}` instead of the
	// AI-Gateway flat keys), keeping only the sub-fields the real DP schema
	// declares for that provider and reporting the rest as dropped (prefixed
	// "<prov>." for consistency with the gcp_environment sub-loop below).
	// A non-map value is reported as the bare provider key: there is no
	// sub-field to blame, and the whole block would otherwise vanish silently.
	mergeNestedBlock := func(prov string, allowed map[string]bool, v any) {
		block, ok := v.(map[string]any)
		if !ok {
			dropped = append(dropped, prov)
			return
		}
		for bk, bv := range block {
			if allowed[bk] {
				addNested(prov, bk, bv)
			} else {
				dropped = append(dropped, prov+"."+bk)
			}
		}
	}

	hasRawBlock := map[string]bool{}
	for k := range opts {
		switch {
		case (providerType == "gemini" || providerType == "vertex") && k == "gemini":
			hasRawBlock["gemini"] = true
		case providerType == "bedrock" && k == "bedrock":
			hasRawBlock["bedrock"] = true
		case providerType == "dashscope" && k == "dashscope":
			hasRawBlock["dashscope"] = true
		case providerType == "kimi" && k == "kimi":
			hasRawBlock["kimi"] = true
		case providerType == "cohere" && k == "cohere":
			hasRawBlock["cohere"] = true
		case providerType == "huggingface" && k == "huggingface":
			hasRawBlock["huggingface"] = true
		case providerType == "databricks" && k == "databricks":
			hasRawBlock["databricks"] = true
		}
	}

	for k, v := range opts {
		switch {
		case providerType == "azure" && k == "deployment_id":
			out["azure_deployment_id"] = v
		case providerType == "azure" && k == "api_version":
			out["azure_api_version"] = v
		case providerType == "anthropic" && k == "version":
			out["anthropic_version"] = v
		case (providerType == "gemini" || providerType == "vertex") && k == "gemini":
			mergeNestedBlock("gemini", aimap.GeminiOptionKeys, v)
		case (providerType == "gemini" || providerType == "vertex") && k == "gcp_environment":
			if hasRawBlock["gemini"] {
				continue // superseded by the raw gemini block above.
			}
			if env, ok := v.(map[string]any); ok {
				for ek, ev := range env {
					switch {
					case aimap.GeminiOptionKeys[ek]:
						addNested("gemini", ek, ev)
					case aimap.ModelOptionKeys[ek]:
						out[ek] = ev
					default:
						dropped = append(dropped, "gemini."+ek)
					}
				}
			}
		case providerType == "bedrock" && k == "bedrock":
			mergeNestedBlock("bedrock", aimap.BedrockNestedOptionKeys, v)
		case providerType == "bedrock" && aimap.BedrockOptionKeys[k]:
			if hasRawBlock["bedrock"] {
				continue // superseded by the raw bedrock block above.
			}
			if k == "region" {
				addNested("bedrock", "aws_region", v)
			} else {
				addNested("bedrock", k, v)
			}
		case providerType == "llama2" && k == "format":
			out["llama2_format"] = v
		case providerType == "mistral" && k == "format":
			out["mistral_format"] = v
		case providerType == "dashscope" && k == "dashscope":
			mergeNestedBlock("dashscope", aimap.DashscopeOptionKeys, v)
		case providerType == "dashscope" && aimap.DashscopeOptionKeys[k]:
			if hasRawBlock["dashscope"] {
				continue
			}
			addNested("dashscope", k, v)
		case providerType == "kimi" && k == "kimi":
			mergeNestedBlock("kimi", aimap.KimiOptionKeys, v)
		case providerType == "kimi" && aimap.KimiOptionKeys[k]:
			if hasRawBlock["kimi"] {
				continue
			}
			addNested("kimi", k, v)
		case providerType == "cohere" && k == "cohere":
			mergeNestedBlock("cohere", aimap.CohereOptionKeys, v)
		case providerType == "cohere" && aimap.CohereOptionKeys[k]:
			if hasRawBlock["cohere"] {
				continue
			}
			addNested("cohere", k, v)
		case providerType == "huggingface" && k == "huggingface":
			mergeNestedBlock("huggingface", aimap.HuggingFaceOptionKeys, v)
		case providerType == "huggingface" && aimap.HuggingFaceOptionKeys[k]:
			if hasRawBlock["huggingface"] {
				continue
			}
			addNested("huggingface", k, v)
		case providerType == "databricks" && k == "databricks":
			mergeNestedBlock("databricks", aimap.DatabricksOptionKeys, v)
		case providerType == "databricks" && aimap.DatabricksOptionKeys[k]:
			if hasRawBlock["databricks"] {
				continue
			}
			addNested("databricks", k, v)
		case aimap.ModelOptionKeys[k]:
			out[k] = v
		default:
			dropped = append(dropped, k)
		}
	}

	if provider != nil {
		if providerType == "anthropic" && out["anthropic_version"] == nil {
			out["anthropic_version"] = "2023-06-01"
		}
		if providerType == "bedrock" && isBedrockAnthropicModelName(modelName) && out["anthropic_version"] == nil {
			out["anthropic_version"] = "bedrock-2023-05-31"
		}
		if providerType == "azure" && provider.Config.Instance != "" {
			out["azure_instance"] = provider.Config.Instance
		}
		if providerType == "bedrock" {
			a := provider.Config.Auth
			if a.AssumeRoleARN != "" {
				addNested("bedrock", "aws_assume_role_arn", a.AssumeRoleARN)
			}
			if a.RoleSessionName != "" {
				addNested("bedrock", "aws_role_session_name", a.RoleSessionName)
			}
			if a.STSEndpointURL != "" {
				addNested("bedrock", "aws_sts_endpoint_url", a.STSEndpointURL)
			}
			if a.BatchRoleARN != "" {
				addNested("bedrock", "batch_role_arn", a.BatchRoleARN)
			}
		}
	}

	for prov, m := range nested {
		out[prov] = m
	}
	if len(out) == 0 {
		out = nil
	}
	sort.Strings(dropped)
	return out, dropped
}

func isBedrockAnthropicModelName(name string) bool {
	return strings.Contains(strings.ToLower(name), "anthropic.claude")
}
