package convert

import (
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

	lowerEmbeddingsModel(emb, provider)

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
func lowerEmbeddingsModel(emb map[string]any, provider *aigw.Provider) {
	model, ok := emb["model"].(map[string]any)
	if !ok {
		return
	}
	config, ok := model["config"].(map[string]any)
	if !ok {
		return
	}
	delete(model, "config")

	providerType, _ := config["type"].(string)
	if providerType == "" && provider != nil {
		providerType = provider.Type
	}
	// Emit the ai-proxy-advanced provider enum (e.g. vertex -> gemini), matching
	// the target path (convert/model.go), rather than the raw AI Gateway type.
	model["provider"] = aimap.PluginProvider(providerType)

	opts := map[string]any{}
	for k, v := range config {
		if k != "type" {
			opts[k] = v
		}
	}
	name, _ := model["name"].(string)
	options := mapOptions(opts, providerType, name, provider)
	if providerType == "azure" {
		options = nestAzureEmbeddingsOptions(options)
	}
	if len(options) > 0 {
		model["options"] = options
	}
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

// lowerVectorDB lowers an AI Gateway vectordb entity block into the
// ai-proxy-advanced plugin shape, inverting revert.vectorDBFromConfig. The
// entity uses a `type` discriminator with the strategy's connection fields
// hoisted to the top level (cluster/keepalive/sentinel/ssl grouped into
// objects); the plugin uses a `strategy` discriminator with those fields inside
// a `redis`/`pgvector` sub-block and the grouped families flattened back to
// cluster_*/keepalive_*/sentinel_*/ssl_* keys.
func lowerVectorDB(vd map[string]any) map[string]any {
	strategy, _ := vd["type"].(string)
	out := map[string]any{}
	if strategy != "" {
		out["strategy"] = strategy
	}
	sub := map[string]any{}
	for k, v := range vd {
		switch k {
		case "type":
			// discriminator becomes `strategy`, already set above.
		case "dimensions", "distance_metric", "threshold":
			out[k] = v
		default:
			sub[k] = v
		}
	}
	switch strategy {
	case "redis":
		unnestVectorDB(sub, "cluster", map[string]string{"nodes": "cluster_nodes"})
		unnestVectorDB(sub, "keepalive", nil)
		unnestVectorDB(sub, "sentinel", map[string]string{"nodes": "sentinel_nodes"})
	case "pgvector":
		flattenPgvectorSSL(sub)
	}
	if strategy != "" && len(sub) > 0 {
		out[strategy] = sub
	}
	return out
}

// unnestVectorDB expands sub[group] (a grouped object like cluster/keepalive/
// sentinel) into flat `group_<field>` keys on sub, deleting the group. Fields
// listed in rename get an explicit flat name (e.g. cluster.nodes ->
// cluster_nodes rather than cluster_nodes via the default); others default to
// `<group>_<field>`.
func unnestVectorDB(sub map[string]any, group string, rename map[string]string) {
	obj, ok := sub[group].(map[string]any)
	if !ok {
		return
	}
	delete(sub, group)
	for k, v := range obj {
		if name, ok := rename[k]; ok {
			sub[name] = v
			continue
		}
		sub[group+"_"+k] = v
	}
}

// flattenPgvectorSSL rewrites the entity's pgvector `ssl` object into the
// plugin's flat ssl_* keys, mapping ssl.enabled back to the `ssl` boolean.
func flattenPgvectorSSL(sub map[string]any) {
	ssl, ok := sub["ssl"].(map[string]any)
	if !ok {
		return
	}
	delete(sub, "ssl")
	names := map[string]string{
		"cert":     "ssl_cert",
		"cert_key": "ssl_cert_key",
		"required": "ssl_required",
		"verify":   "ssl_verify",
		"version":  "ssl_version",
	}
	for k, v := range ssl {
		if k == "enabled" {
			sub["ssl"] = v
			continue
		}
		if name, ok := names[k]; ok {
			sub[name] = v
			continue
		}
		sub[k] = v
	}
}

// mapOptions translates a target model's option map into an ai-proxy-advanced
// model.options map. It renames/nests provider-specific keys per provider type
// and folds in provider-level fields (azure instance, gemini project id, bedrock
// assume-role auth). Keys not handled specially pass through flat.
func mapOptions(opts map[string]any, providerType, modelName string, provider *aigw.Provider) map[string]any {
	out := map[string]any{}
	nested := map[string]map[string]any{}
	addNested := func(prov, key string, val any) {
		if nested[prov] == nil {
			nested[prov] = map[string]any{}
		}
		nested[prov][key] = val
	}

	for k, v := range opts {
		switch {
		case providerType == "azure" && k == "deployment_id":
			out["azure_deployment_id"] = v
		case providerType == "azure" && k == "api_version":
			out["azure_api_version"] = v
		case providerType == "anthropic" && k == "version":
			out["anthropic_version"] = v
		case (providerType == "gemini" || providerType == "vertex") && k == "gcp_environment":
			if env, ok := v.(map[string]any); ok {
				for ek, ev := range env {
					if aimap.GeminiOptionKeys[ek] {
						addNested("gemini", ek, ev)
					} else {
						out[ek] = ev
					}
				}
			}
		case providerType == "bedrock" && aimap.BedrockOptionKeys[k]:
			if k == "region" {
				addNested("bedrock", "aws_region", v)
			} else {
				addNested("bedrock", k, v)
			}
		case providerType == "llama2" && k == "format":
			out["llama2_format"] = v
		case providerType == "mistral" && k == "format":
			out["mistral_format"] = v
		case providerType == "dashscope" && k == "international":
			addNested("dashscope", k, v)
		case providerType == "kimi" && k == "international":
			addNested("kimi", k, v)
		case providerType == "cohere" &&
			(k == "embedding_input_type" || k == "wait_for_model" || k == "api_version"):
			addNested("cohere", k, v)
		case providerType == "huggingface" && (k == "use_cache" || k == "wait_for_model"):
			addNested("huggingface", k, v)
		case providerType == "databricks" && k == "workspace_instance_id":
			addNested("databricks", k, v)
		default:
			out[k] = v
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
		return nil
	}
	return out
}

func isBedrockAnthropicModelName(name string) bool {
	return strings.Contains(strings.ToLower(name), "anthropic.claude")
}
