package convert

import "github.com/gperanich/ai-deck-converter/internal/aigw"

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

// geminiOptionKeys are target-config keys that nest under model.options.gemini
// for the gemini and vertex provider types.
var geminiOptionKeys = map[string]bool{
	"location_id": true, "api_endpoint": true, "endpoint_id": true,
}

// bedrockOptionKeys are target-config keys that nest under model.options.bedrock.
var bedrockOptionKeys = map[string]bool{
	"aws_region": true, "embeddings_normalize": true, "video_output_s3_uri": true,
	"batch_bucket_prefix": true, "batch_role_arn": true, "performance_config_latency": true,
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
		case (providerType == "gemini" || providerType == "vertex") && geminiOptionKeys[k]:
			addNested("gemini", k, v)
		case providerType == "bedrock" && bedrockOptionKeys[k]:
			addNested("bedrock", k, v)
		default:
			out[k] = v
		}
	}

	if provider != nil {
		if providerType == "azure" && provider.Config.Instance != "" {
			out["azure_instance"] = provider.Config.Instance
		}
		if (providerType == "gemini" || providerType == "vertex") && provider.Config.ProjectID != "" {
			addNested("gemini", "project_id", provider.Config.ProjectID)
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
