package convert

import (
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

// resolveEmbeddings folds a referenced provider's auth into the embeddings
// block. When `embeddings.provider` names a top-level provider entity, its auth
// is resolved via resolveAuth and merged into `embeddings.auth` (any explicitly
// configured auth keys win), then the provider reference is dropped — the
// ai-proxy-advanced embeddings schema has no top-level provider field.
func (c *Converter) resolveEmbeddings(raw any) (any, error) {
	emb, ok := raw.(map[string]any)
	if !ok {
		return raw, nil
	}
	provName, ok := emb["provider"].(string)
	if !ok || provName == "" {
		return emb, nil
	}
	delete(emb, "provider")

	provider := c.providers[provName]
	if provider == nil {
		if err := c.warn("model embeddings reference unknown provider %q; auth may be incomplete", provName); err != nil {
			return nil, err
		}
		return emb, nil
	}

	mapEmbeddingsOptions(emb, provider)

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

// mapEmbeddingsOptions folds a referenced provider's non-auth, provider-specific
// fields into the embeddings model's options block. The embeddings schema nests
// these under model.options.<provider> with bare keys (e.g.
// model.options.azure.instance) — distinct from mapOptions, which emits flat
// azure_* keys for ai-proxy-advanced targets. Currently only the azure instance
// is mapped; extend the switch as more provider fields are surfaced.
func mapEmbeddingsOptions(emb map[string]any, provider *aigw.Provider) {
	model, ok := emb["model"].(map[string]any)
	if !ok {
		return
	}
	providerType, _ := model["provider"].(string)
	if providerType == "" {
		providerType = provider.Type
	}
	switch providerType {
	case "azure":
		if provider.Config.Instance != "" {
			embeddingsNested(model, "azure")["instance"] = provider.Config.Instance
		}
	case "bedrock":
		if opts, ok := model["options"].(map[string]any); ok {
			if b, ok := opts["bedrock"].(map[string]any); ok {
				if v, ok := b["region"]; ok {
					b["aws_region"] = v
					delete(b, "region")
				}
			}
		}
	}
}

// embeddingsNested returns model.options.<prov>, creating the options map and the
// provider sub-map if absent.
func embeddingsNested(model map[string]any, prov string) map[string]any {
	options, ok := model["options"].(map[string]any)
	if !ok {
		options = map[string]any{}
		model["options"] = options
	}
	sub, ok := options[prov].(map[string]any)
	if !ok {
		sub = map[string]any{}
		options[prov] = sub
	}
	return sub
}

// mapOptions translates a target model's option map into an ai-proxy-advanced
// model.options map. It renames/nests provider-specific keys per provider type
// and folds in provider-level fields (azure instance, gemini project id, bedrock
// assume-role auth). Keys not handled specially pass through flat.
func mapOptions(opts map[string]any, providerType string, provider *aigw.Provider) map[string]any {
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
