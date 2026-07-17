package revert

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// detectProviderType maps an ai-proxy-advanced provider enum back to an AI
// Gateway provider type. The only ambiguous enum is "gemini", which serves
// both the gemini and vertex provider types; the route path is the deciding
// signal (vertex routes carry project/location URL templates, gemini routes do
// not). Aliases and options are no help: both provider types share them.
func detectProviderType(enum, routePath string) string {
	if enum != "gemini" {
		return enum
	}
	if strings.Contains(routePath, "/projects/") && strings.Contains(routePath, "/locations/") {
		return "vertex"
	}
	return "gemini"
}

// defoldedTarget is the result of pulling a single ai-proxy-advanced target
// apart into its AI Gateway pieces: the target-level fields, the remaining
// target options, and the provider-level fields to synthesize a Provider from.
type defoldedTarget struct {
	auth          aigw.ProviderAuth
	allowOverride *bool
	instance      string // azure
	projectID     string // gemini / vertex
	options       map[string]any
}

// defoldAuth reverses convert's resolveAuth: it reads an ai-proxy-advanced
// target auth block back into provider auth fields plus the target-level
// allow_override flag, inferring the auth type from which fields are present.
func defoldAuth(auth map[string]any) (aigw.ProviderAuth, *bool) {
	var a aigw.ProviderAuth
	if name, value := getStr(auth, "header_name"), getStr(auth, "header_value"); name != "" || value != "" {
		a.Headers = []aigw.AuthHeader{{Name: name, Value: value}}
	}
	if name, value, loc := getStr(auth, "param_name"), getStr(auth, "param_value"),
		getStr(auth, "param_location"); name != "" || value != "" || loc != "" {
		a.Params = []aigw.AuthParam{{Name: name, Value: value, Location: loc}}
	}
	a.AccessKeyID = getStr(auth, "aws_access_key_id")
	a.SecretAccessKey = getStr(auth, "aws_secret_access_key")
	a.ClientID = getStr(auth, "azure_client_id")
	a.ClientSecret = getStr(auth, "azure_client_secret")
	a.TenantID = getStr(auth, "azure_tenant_id")
	a.UseManagedIdentity = getBool(auth, "azure_use_managed_identity")
	a.ServiceAccountJSON = getStr(auth, "gcp_service_account_json")
	a.UseGCPServiceAccount = getBool(auth, "gcp_use_service_account")
	a.MetadataURL = getStr(auth, "gcp_metadata_url")
	a.OAuthTokenURL = getStr(auth, "gcp_oauth_token_url")

	// Only a *true* flag signals its auth type — the source spells out every
	// flag (including the irrelevant false ones), so treating non-nil as a
	// signal misdetects e.g. a gcp provider carrying azure_use_managed_identity:
	// false as azure. Normalize false back to nil so it neither drives
	// inference nor is emitted.
	if !boolVal(a.UseManagedIdentity) {
		a.UseManagedIdentity = nil
	}
	if !boolVal(a.UseGCPServiceAccount) {
		a.UseGCPServiceAccount = nil
	}

	switch {
	case len(a.Headers) > 0 || len(a.Params) > 0:
		a.Type = "basic"
	case a.AccessKeyID != "" || a.SecretAccessKey != "":
		a.Type = "aws"
	case a.ClientID != "" || a.UseManagedIdentity != nil:
		a.Type = "azure"
	case a.ServiceAccountJSON != "" || a.UseGCPServiceAccount != nil || a.MetadataURL != "":
		a.Type = "gcp"
	}
	// Forward emits gcp_use_service_account=true implicitly for gcp auth; drop
	// the explicit flag again when it matches that inference so it round-trips.
	if a.Type == "gcp" && a.UseGCPServiceAccount != nil && *a.UseGCPServiceAccount &&
		(a.ServiceAccountJSON != "") {
		a.UseGCPServiceAccount = nil
	}
	return a, getBool(auth, "allow_override")
}

// defoldOptions reverses convert's mapOptions: it un-nests the provider-scoped
// option blocks (gemini, bedrock), undoes the provider-specific renames, and
// hoists provider-level fields out of the option map.
func defoldOptions(options map[string]any, providerType string, d *defoldedTarget) {
	out := map[string]any{}
	for k, v := range options {
		switch {
		case providerType == "azure" && k == "azure_deployment_id":
			out["deployment_id"] = v
		case providerType == "azure" && k == "azure_api_version":
			out["api_version"] = v
		case providerType == "azure" && k == "azure_instance":
			d.instance, _ = v.(string)
		case providerType == "anthropic" && k == "anthropic_version":
			out["version"] = v
		case (providerType == "gemini" || providerType == "vertex") && k == "gemini":
			block, _ := v.(map[string]any)
			if len(block) > 0 {
				env := make(map[string]any, len(block))
				for gk, gv := range block {
					env[gk] = gv
				}
				out["gcp_environment"] = env
			}
		case providerType == "bedrock" && k == "bedrock":
			block, _ := v.(map[string]any)
			for bk, bv := range block {
				switch {
				case bk == "aws_assume_role_arn":
					d.auth.AssumeRoleARN, _ = bv.(string)
				case bk == "aws_role_session_name":
					d.auth.RoleSessionName, _ = bv.(string)
				case bk == "aws_sts_endpoint_url":
					d.auth.STSEndpointURL, _ = bv.(string)
				case bk == "aws_region":
					out["region"] = bv
				case aimap.BedrockOptionKeys[bk]:
					// Target-level option (batch_role_arn, ...).
					out[bk] = bv
				default:
					out[bk] = bv
				}
			}
		default:
			out[k] = v
		}
	}
	if len(out) > 0 {
		d.options = out
	}
}

// defoldTarget splits one ai-proxy-advanced target into target-level and
// provider-level pieces.
func defoldTarget(target map[string]any, providerType string) defoldedTarget {
	var d defoldedTarget
	d.auth, d.allowOverride = defoldAuth(getMap(target, "auth"))
	defoldOptions(getMap(getMap(target, "model"), "options"), providerType, &d)
	return d
}

// embeddingsFromConfig reverses convert.resolveEmbeddings: it de-folds an
// ai-proxy-advanced config.balancer.embeddings block (auth + model.{provider,
// name, options}) into the AI Gateway entity shape (allow_auth_override,
// provider reference, model.{name, config}). The embeddings model is treated
// like a target — auth becomes a synthesized model_provider, options become the
// model's config block, and provider-level fields (azure instance) are hoisted
// onto the provider.
func (r *Reverter) embeddingsFromConfig(emb map[string]any) map[string]any {
	model := getMap(emb, "model")
	providerType := detectProviderType(getStr(model, "provider"), "")

	var d defoldedTarget
	d.auth, d.allowOverride = defoldAuth(getMap(emb, "auth"))
	defoldOptions(flattenEmbeddingsOptions(getMap(model, "options")), providerType, &d)

	config := map[string]any{"type": providerType}
	for k, v := range d.options {
		config[k] = v
	}

	out := map[string]any{
		"provider": r.providerFor(providerType, &d),
		"model": map[string]any{
			"name":   getStr(model, "name"),
			"config": config,
		},
	}
	if d.allowOverride != nil {
		out["allow_auth_override"] = *d.allowOverride
	}
	return out
}

// flattenEmbeddingsOptions converts the nested embeddings option shape produced
// by convert.nestAzureEmbeddingsOptions (azure.{instance,...}) back into the
// flat azure_* keys defoldOptions expects, and drops the empty provider-block
// siblings (bedrock/gemini/huggingface: null) the plugin schema emits.
func flattenEmbeddingsOptions(options map[string]any) map[string]any {
	flat := map[string]any{}
	for k, v := range options {
		if v == nil {
			continue
		}
		if k == "azure" {
			if azure, ok := v.(map[string]any); ok {
				for ak, av := range azure {
					flat["azure_"+ak] = av
				}
				continue
			}
		}
		flat[k] = v
	}
	return flat
}

// vectorDBFromConfig converts an ai-proxy-advanced config.balancer.vectordb
// block into the AI Gateway entity shape: the `strategy` discriminator becomes
// `type`, dimensions/distance_metric/threshold stay at the top, and the
// selected strategy's sub-block is hoisted up with its flat redis keys nested
// into cluster/keepalive/sentinel objects (pgvector ssl_* keys into an ssl
// object). Nil values are dropped so the strict (additionalProperties:false)
// entity schema accepts the result.
func vectorDBFromConfig(vd map[string]any) map[string]any {
	strategy := getStr(vd, "strategy")
	out := map[string]any{}
	if strategy != "" {
		out["type"] = strategy
	}
	for _, k := range []string{"dimensions", "distance_metric", "threshold"} {
		if v := vd[k]; v != nil {
			out[k] = v
		}
	}
	sub := getMap(vd, strategy)
	switch strategy {
	case "redis":
		hoistRedis(sub, out)
	case "pgvector":
		hoistPgvector(sub, out)
	}
	return out
}

// putNested assigns out[group][field] = val (creating the group), skipping nils.
func putNested(out map[string]any, group, field string, val any) {
	if val == nil {
		return
	}
	m, _ := out[group].(map[string]any)
	if m == nil {
		m = map[string]any{}
		out[group] = m
	}
	m[field] = val
}

// hoistRedis maps the flat ai-proxy-advanced redis keys onto the entity's redis
// vectordb fields, nesting the cluster_/keepalive_/sentinel_ families.
func hoistRedis(sub, out map[string]any) {
	nested := map[string][2]string{
		"cluster_max_redirections": {"cluster", "max_redirections"},
		"cluster_nodes":            {"cluster", "nodes"},
		"cluster_addresses":        {"cluster", "nodes"},
		"keepalive_backlog":        {"keepalive", "backlog"},
		"keepalive_pool_size":      {"keepalive", "pool_size"},
		"sentinel_master":          {"sentinel", "master"},
		"sentinel_nodes":           {"sentinel", "nodes"},
		"sentinel_addresses":       {"sentinel", "nodes"},
		"sentinel_password":        {"sentinel", "password"},
		"sentinel_role":            {"sentinel", "role"},
		"sentinel_username":        {"sentinel", "username"},
	}
	// timeout has no entity equivalent (connect/read/send_timeout carry it).
	drop := map[string]bool{"timeout": true}
	for k, v := range sub {
		if v == nil || drop[k] {
			continue
		}
		if dst, ok := nested[k]; ok {
			putNested(out, dst[0], dst[1], v)
			continue
		}
		out[k] = v
	}
}

// hoistPgvector maps the flat ai-proxy-advanced pgvector keys onto the entity's
// pgvector vectordb fields, nesting the ssl_* family into an ssl object.
func hoistPgvector(sub, out map[string]any) {
	nested := map[string][2]string{
		"ssl_cert":     {"ssl", "cert"},
		"ssl_cert_key": {"ssl", "cert_key"},
		"ssl_required": {"ssl", "required"},
		"ssl_verify":   {"ssl", "verify"},
		"ssl_version":  {"ssl", "version"},
	}
	for k, v := range sub {
		if v == nil {
			continue
		}
		if k == "ssl" { // plugin boolean -> ssl.enabled
			putNested(out, "ssl", "enabled", v)
			continue
		}
		if dst, ok := nested[k]; ok {
			putNested(out, dst[0], dst[1], v)
			continue
		}
		out[k] = v
	}
}

// vaultRefRe extracts the vault prefix from a "{vault://prefix/key}" reference.
var vaultRefRe = regexp.MustCompile(`\{vault://([^/}]+)/`)

// providerFor returns the name of a synthesized Provider for the de-folded
// target, deduping identical providers by fingerprint. Names derive from the
// vault prefix in the auth credential when present ("openai-env"), otherwise
// from a per-type counter ("openai-1").
func (r *Reverter) providerFor(providerType string, d *defoldedTarget) string {
	fp := providerFingerprint(providerType, d)
	if name, ok := r.providerByFP[fp]; ok {
		return name
	}

	name := r.uniqueProviderName(providerType, d)
	auth := d.auth
	// The current Konnect SDK rejects config.auth.use_managed_identity /
	// use_gcp_service_account outright, so they cannot be migrated. A true flag
	// signals a keyless auth mode (azure managed identity / gcp service
	// account) that must be reconfigured manually; drop it and warn.
	if boolVal(auth.UseManagedIdentity) {
		r.warn("provider %q: azure managed-identity auth is not migratable; configure it manually", name) //nolint:errcheck
	}
	if boolVal(auth.UseGCPServiceAccount) {
		r.warn("provider %q: gcp service-account auth is not migratable; configure it manually", name) //nolint:errcheck
	}
	auth.UseManagedIdentity = nil
	auth.UseGCPServiceAccount = nil
	p := aigw.Provider{
		Type: providerType,
		Name: name,
		Config: aigw.ProviderConfig{
			Auth:      auth,
			Instance:  d.instance,
			ProjectID: d.projectID,
		},
	}
	r.providers = append(r.providers, p)
	r.providerByFP[fp] = name
	return name
}

// providerFingerprint builds a canonical identity string for provider dedup.
func providerFingerprint(providerType string, d *defoldedTarget) string {
	a := d.auth
	parts := []string{
		"type=" + providerType,
		"auth.type=" + a.Type,
		"instance=" + d.instance,
		"project_id=" + d.projectID,
		"access_key_id=" + a.AccessKeyID,
		"secret_access_key=" + a.SecretAccessKey,
		"assume_role_arn=" + a.AssumeRoleARN,
		"role_session_name=" + a.RoleSessionName,
		"sts_endpoint_url=" + a.STSEndpointURL,
		"batch_role_arn=" + a.BatchRoleARN,
		"client_id=" + a.ClientID,
		"client_secret=" + a.ClientSecret,
		"tenant_id=" + a.TenantID,
		"service_account_json=" + a.ServiceAccountJSON,
		"metadata_url=" + a.MetadataURL,
		"oauth_token_url=" + a.OAuthTokenURL,
		fmt.Sprintf("use_managed_identity=%v", ptrVal(a.UseManagedIdentity)),
		fmt.Sprintf("use_gcp_service_account=%v", ptrVal(a.UseGCPServiceAccount)),
	}
	for _, h := range a.Headers {
		parts = append(parts, "header="+h.Name+"="+h.Value)
	}
	for _, p := range a.Params {
		parts = append(parts, "param="+p.Name+"="+p.Value+"@"+p.Location)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func ptrVal(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func boolVal(b *bool) bool {
	return b != nil && *b
}

// uniqueProviderName derives a stable, human-readable provider name.
func (r *Reverter) uniqueProviderName(providerType string, d *defoldedTarget) string {
	base := ""
	if prefix := vaultPrefix(d.auth); prefix != "" {
		base = providerType + "-" + prefix
	}
	if base == "" || r.providerNames[base] || r.usedNames[base] {
		for {
			r.providerCounts[providerType]++
			candidate := fmt.Sprintf("%s-%d", providerType, r.providerCounts[providerType])
			if !r.providerNames[candidate] && !r.usedNames[candidate] {
				base = candidate
				break
			}
		}
	}
	r.providerNames[base] = true
	r.usedNames[base] = true
	return base
}

// vaultPrefix returns the vault prefix referenced by the first credential
// value in the auth, if any.
func vaultPrefix(a aigw.ProviderAuth) string {
	candidates := []string{a.SecretAccessKey, a.AccessKeyID, a.ServiceAccountJSON, a.ClientSecret}
	if len(a.Headers) > 0 {
		candidates = append([]string{a.Headers[0].Value}, candidates...)
	}
	if len(a.Params) > 0 {
		candidates = append([]string{a.Params[0].Value}, candidates...)
	}
	for _, v := range candidates {
		if m := vaultRefRe.FindStringSubmatch(v); m != nil {
			return m[1]
		}
	}
	return ""
}
