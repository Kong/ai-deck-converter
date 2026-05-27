package aigw

import "gopkg.in/yaml.v3"

// Provider is an AI Gateway provider (an upstream LLM provider with auth). It is
// not emitted as a standalone Kong entity; its type and auth are folded into each
// ai-proxy-advanced target that references it by name.
type Provider struct {
	Type        string         `yaml:"type"`
	DisplayName string         `yaml:"display_name"`
	Name        string         `yaml:"name"`
	Labels      Labels         `yaml:"labels"`
	Config      ProviderConfig `yaml:"config"`
}

// ProviderConfig holds the provider auth plus provider-specific top-level fields.
type ProviderConfig struct {
	Auth      ProviderAuth `yaml:"auth"`
	Instance  string       `yaml:"instance"`   // azure
	ProjectID string       `yaml:"project_id"` // gemini / vertex
}

// UnmarshalYAML decodes a ProviderConfig and tolerates the flattened auth form
// (`config: {type: basic, headers: [...]}` with no `auth:` wrapper) by reading
// the auth fields directly off config when `config.auth` is absent.
func (c *ProviderConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawConfig ProviderConfig
	var rc rawConfig
	if err := node.Decode(&rc); err != nil {
		return err
	}
	*c = ProviderConfig(rc)
	if c.Auth.isEmpty() {
		var flat ProviderAuth
		if err := node.Decode(&flat); err == nil {
			c.Auth = flat
		}
	}
	return nil
}

// ProviderAuth is the union of all provider auth variants (basic/aws/azure/gcp).
// The variants have non-overlapping field names, so a single superset struct
// decodes any of them; Type selects which fields are meaningful.
type ProviderAuth struct {
	Type    string       `yaml:"type"`
	Headers []AuthHeader `yaml:"headers"` // basic
	Params  []AuthParam  `yaml:"params"`  // basic

	// aws
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	AssumeRoleARN   string `yaml:"assume_role_arn"`
	RoleSessionName string `yaml:"role_session_name"`
	STSEndpointURL  string `yaml:"sts_endpoint_url"`
	BatchRoleARN    string `yaml:"batch_role_arn"`

	// azure
	ClientID           string `yaml:"client_id"`
	ClientSecret       string `yaml:"client_secret"`
	TenantID           string `yaml:"tenant_id"`
	UseManagedIdentity *bool  `yaml:"use_managed_identity"`

	// gcp
	ServiceAccountJSON   string `yaml:"service_account_json"`
	MetadataURL          string `yaml:"metadata_url"`
	OAuthTokenURL        string `yaml:"oauth_token_url"`
	UseGCPServiceAccount *bool  `yaml:"use_gcp_service_account"`
}

// isEmpty reports whether no auth was decoded (used to detect the flattened form).
func (a ProviderAuth) isEmpty() bool {
	return a.Type == "" && len(a.Headers) == 0 && len(a.Params) == 0 &&
		a.AccessKeyID == "" && a.ServiceAccountJSON == "" &&
		a.ClientID == "" && a.UseManagedIdentity == nil && a.UseGCPServiceAccount == nil
}

// AuthHeader is a single auth header (maxItems 1 in the schema).
type AuthHeader struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// AuthParam is a single auth query/body param (maxItems 1 in the schema).
type AuthParam struct {
	Name     string `yaml:"name"`
	Value    string `yaml:"value"`
	Location string `yaml:"location"` // body | query
}
