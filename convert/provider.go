package convert

import (
	"fmt"
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
		if p.Type != "sagemaker" && a.AWSSessionToken != "" {
			auth["aws_session_token"] = a.AWSSessionToken
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
		} else if a.Type == "gcp" && a.ServiceAccountJSON != "" {
			auth["gcp_use_service_account"] = true
		}
		if a.MetadataURL != "" {
			auth["gcp_metadata_url"] = a.MetadataURL
		}
		if a.OAuthTokenURL != "" {
			auth["gcp_oauth_token_url"] = a.OAuthTokenURL
		}
		// gcp workload identity federation. The credentials of the federated
		// source identity (currently only aws-iam) are exchanged for a GCP token,
		// so they occupy the same target auth keys the AWS providers use.
		if wif := a.WorkloadIdentityFederation; wif != nil {
			if wif.Source != "" {
				auth["gcp_workload_identity_federation_source"] = wif.Source
			}
			if wif.AuthJSON != "" {
				auth["gcp_workload_identity_federation_auth_json"] = wif.AuthJSON
			}
			if wif.AWSAccessKeyID != "" {
				auth["aws_access_key_id"] = wif.AWSAccessKeyID
			}
			if wif.AWSSecretAccessKey != "" {
				auth["aws_secret_access_key"] = wif.AWSSecretAccessKey
			}
			if wif.AWSSessionToken != "" {
				auth["aws_session_token"] = wif.AWSSessionToken
			}
		}
		// sagemaker
		if p.Type == "sagemaker" && a.SessionToken != "" {
			auth["aws_session_token"] = a.SessionToken
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
//
// It works on a copy. A model pass can run more than once over the source.
func (c *Converter) resolveEmbeddings(src ConversionIssueSource, raw any) any {
	emb, ok := deepCopy(raw).(map[string]any)
	if !ok {
		return raw
	}

	var provider *aigw.Provider
	if provName, _ := emb["provider"].(string); provName != "" {
		delete(emb, "provider")
		provider = c.providers[provName]
		if provider == nil {
			c.warn(src.at("config.balancer.embeddings.provider"), UndefinedReference,
				"model embeddings reference unknown provider %q; auth may be incomplete", provName)
		}
	}

	lowerEmbeddingsModel(emb, provider)

	if provider == nil {
		return emb
	}
	resolved := resolveAuth(provider, nil)
	if resolved == nil {
		return emb
	}
	existing, _ := emb["auth"].(map[string]any)
	if existing == nil {
		emb["auth"] = resolved
		return emb
	}
	for k, v := range resolved {
		if _, set := existing[k]; !set {
			existing[k] = v
		}
	}
	return emb
}

// deepCopy copies the maps and slices of a YAML value.
func deepCopy(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = deepCopy(e)
		}
		return out
	default:
		return v
	}
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
	options := mapOptions(opts, providerType, name, provider, false)
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

// mapOptions translates a target model's option map into an ai-proxy-advanced
// model.options map. It renames/nests provider-specific keys per provider type
// and folds in provider-level fields (azure instance, gemini project id, bedrock
// assume-role auth). Keys not handled specially pass through flat. passthrough
// drops the defaults the plugin only needs when it rewrites the request: a
// passthrough target forwards the client's body as it stands, so the client
// supplies them.
func mapOptions(
	opts map[string]any, providerType, modelName string, provider *aigw.Provider, passthrough bool,
) map[string]any {
	out := map[string]any{}
	nested := map[string]map[string]any{}
	addNested := func(prov, key string, val any) {
		if nested[prov] == nil {
			nested[prov] = map[string]any{}
		}
		nested[prov][key] = val
	}
	appendNested := func(prov, key string, val any) {
		if nested[prov] == nil {
			nested[prov] = map[string]any{}
		}
		for k, v := range val.(map[string]any) {
			nested[prov][key+"_"+k] = v
		}
	}

	for k, v := range opts {
		switch {
		case providerType == "azure" && k == "deployment_id":
			out["azure_deployment_id"] = v
		case providerType == "azure" && k == "api_version":
			out["azure_api_version"] = v
		case providerType == "azure" && k == "foundry_path_prefix":
			out["azure_foundry_path_prefix"] = v
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
		case providerType == "sagemaker" && (k == "aws" || k == "target"):
			appendNested("sagemaker", k, v)
		default:
			out[k] = v
		}
	}

	if provider != nil {
		// anthropic_version is the header the plugin sets when it builds the upstream
		// request; in passthrough the client's own request carries it, and the schema
		// stops requiring the field there, so defaulting it would override the client.
		if providerType == "anthropic" && out["anthropic_version"] == nil && !passthrough {
			out["anthropic_version"] = "2023-06-01"
		}
		if providerType == "bedrock" && isBedrockAnthropicModelName(modelName) &&
			out["anthropic_version"] == nil && !passthrough {
			out["anthropic_version"] = "bedrock-2023-05-31"
		}
		if providerType == "azure" && provider.Config.Instance != "" {
			out["azure_instance"] = provider.Config.Instance
		}
		if providerType == "azure" && provider.Config.Service == "azure-foundry" {
			out["azure_service"] = "azure-foundry"
		}
		if providerType == "azure" && provider.Config.Foundry != nil {
			if provider.Config.Foundry.Resource != "" {
				out["azure_foundry_resource"] = provider.Config.Foundry.Resource
			}
			if provider.Config.Foundry.Domain != "" {
				out["azure_foundry_domain"] = provider.Config.Foundry.Domain
			}
		}
		// A gemini/vertex provider federating from an AWS IAM identity signs the
		// token exchange with AWS, so its region and assume-role settings ride in
		// the same model.options.bedrock block the bedrock provider uses.
		if providerType == "gemini" || providerType == "vertex" {
			if wif := provider.Config.Auth.WorkloadIdentityFederation; wif != nil {
				if wif.AWSRegion != "" {
					addNested("bedrock", "aws_region", wif.AWSRegion)
				}
				if wif.AWSAssumeRoleARN != "" {
					addNested("bedrock", "aws_assume_role_arn", wif.AWSAssumeRoleARN)
				}
				if wif.AWSRoleSessionName != "" {
					addNested("bedrock", "aws_role_session_name", wif.AWSRoleSessionName)
				}
				if wif.AWSSTSEndpointURL != "" {
					addNested("bedrock", "aws_sts_endpoint_url", wif.AWSSTSEndpointURL)
				}
			}
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

// tokenVaultExchangePlugin is the plugin that a provider's token_vault lowers
// into.
const tokenVaultExchangePlugin = "token-vault-exchange" //nolint:gosec // a plugin name, not a credential

// tokenVaultProblem is a token_vault check that a provider fails.
// Field is the path of the failing provider field.
type tokenVaultProblem struct {
	Field  string
	Code   ConversionIssueCode
	Reason string
}

// providerTokenVaultDatastore returns the token-vault Datastore of provider p.
// It returns the first check that p fails instead. owner names the referencing
// entity in the reason.
func (c *Converter) providerTokenVaultDatastore(
	owner string, p *aigw.Provider,
) (*aigw.Datastore, *tokenVaultProblem) {
	if resolveAuth(p, nil) != nil {
		return nil, &tokenVaultProblem{
			Field: "token_vault", Code: ConfigFieldInvalid,
			Reason: fmt.Sprintf(
				"%s: token_vault and credentials in config.auth are mutually exclusive", owner),
		}
	}
	if p.TokenVault.Provider == "" {
		return nil, &tokenVaultProblem{
			Field: "token_vault", Code: MissingRequiredConfigField,
			Reason: fmt.Sprintf("%s: token_vault requires provider", owner),
		}
	}
	if len(p.Datastores) != 1 {
		return nil, &tokenVaultProblem{
			Field: "datastores", Code: MissingRequiredConfigField,
			Reason: fmt.Sprintf("%s: token_vault requires exactly one %q datastore",
				owner, aimap.DatastoreTypeTokenVault),
		}
	}
	ds := c.datastores[p.Datastores[0].Name]
	if ds == nil {
		return nil, &tokenVaultProblem{
			Field: "datastores[0].name", Code: UndefinedReference,
			Reason: fmt.Sprintf("%s references unknown datastore %q", owner, p.Datastores[0].Name),
		}
	}
	if ds.Type != aimap.DatastoreTypeTokenVault {
		return nil, &tokenVaultProblem{
			Field: "datastores[0].name", Code: ConfigFieldInvalid,
			Reason: fmt.Sprintf("%s: token_vault does not support a %q datastore; use %q instead",
				owner, ds.Type, aimap.DatastoreTypeTokenVault),
		}
	}
	return ds, nil
}

// validateModelProviderTokenVaults reports the token_vault problems of every
// model provider, also of a provider that no model targets.
// The output keeps no callback route of an unused provider.
func (c *Converter) validateModelProviderTokenVaults() {
	for i := range c.src.ModelProviders {
		p := &c.src.ModelProviders[i]
		if p.TokenVault == nil {
			continue
		}
		src := issueSource(kindModelProvider, p.Name)
		owner := fmt.Sprintf("provider %q", p.Name)
		snapshot := *c.out
		ds, problem := c.providerTokenVaultDatastore(owner, p)
		if problem != nil {
			c.reportError(src.at(problem.Field), problem.Code, "%s", problem.Reason)
		} else {
			// A model that targets p adds this route during the model pass.
			_ = c.ensureTokenVaultCallbackRoute(src.at("datastores[0].name"), owner, ds,
				source(kindModelProvider, p.Name, "token_vault"))
		}
		*c.out = snapshot
	}
}

// providerTokenVault returns the token-vault-exchange config of the provider
// of target j, or nil if the provider has no token_vault. The plugin replaces
// the target's credential. A provider with static auth cannot also have
// token_vault.
func (c *Converter) providerTokenVault(
	src ConversionIssueSource, m *aigw.Model, j int, p *aigw.Provider,
) (map[string]any, error) {
	at := src.at(fmt.Sprintf("targets[%d].provider", j))
	owner := fmt.Sprintf("model %q target %q: provider %q", m.Name, m.TargetModels[j].Name, p.Name)
	tv := p.TokenVault
	if tv == nil {
		if len(p.Datastores) > 0 {
			return nil, c.fail(at, ConfigFieldInvalid,
				"%s references datastore %q, but datastores require token_vault",
				owner, p.Datastores[0].Name)
		}
		return nil, nil
	}
	if !c.supports(aimap.FeatureProviderTokenVault) {
		return nil, c.failUnsupported(at, aimap.FeatureProviderTokenVault, "provider token_vault")
	}
	ds, problem := c.providerTokenVaultDatastore(owner, p)
	if problem != nil {
		// The model target names the reference that needs the change.
		return nil, c.fail(at, problem.Code, "%s", problem.Reason)
	}
	cfg, err := aimap.ApplyTokenVaultDatastore(aimap.TokenVaultToPlugin(tv), ds)
	if err != nil {
		return nil, c.fail(at, ConfigFieldInvalid, "%s: %v", owner, err)
	}
	routeSource := source(kindModel, m.Name, fmt.Sprintf("targets[%d].provider", j))
	if err := c.ensureTokenVaultCallbackRoute(at, owner, ds, routeSource); err != nil {
		return nil, err
	}
	return cfg, nil
}

// checkEmbeddingsTokenVault fails the model when its embeddings provider has
// token_vault. ai-proxy-advanced sends the embeddings request itself.
// token-vault-exchange cannot set the credential of that request.
func (c *Converter) checkEmbeddingsTokenVault(src ConversionIssueSource, m *aigw.Model) error {
	emb, _ := balancerExtra(m.Config.Balancer, "embeddings").(map[string]any)
	name, _ := emb["provider"].(string)
	if p := c.providers[name]; p != nil && p.TokenVault != nil {
		return c.fail(src.at("config.balancer.embeddings.provider"), ConfigFieldInvalid,
			"model %q: embeddings provider %q has token_vault, but %s cannot set "+
				"the credential of the embeddings request", m.Name, name, tokenVaultExchangePlugin)
	}
	return nil
}
