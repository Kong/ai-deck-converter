package aigw

import "gopkg.in/yaml.v3"

// Provider is an AI Gateway provider (an upstream LLM provider with auth). It is
// not emitted as a standalone Kong entity; its type and auth are folded into each
// ai-proxy-advanced target that references it by name.
type Provider struct {
	Type        string         `yaml:"type,omitempty"`
	DisplayName string         `yaml:"display_name,omitempty"`
	Name        string         `yaml:"name,omitempty"`
	Labels      Labels         `yaml:"labels,omitempty"`
	Config      ProviderConfig `yaml:"config,omitempty"`
}

// ProviderConfig holds the provider auth plus provider-specific top-level fields.
type ProviderConfig struct {
	Auth      ProviderAuth `yaml:"auth,omitempty"`
	Instance  string       `yaml:"instance,omitempty"`   // azure
	ProjectID string       `yaml:"project_id,omitempty"` // gemini / vertex
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
	Type    string       `yaml:"type,omitempty"`
	Headers []AuthHeader `yaml:"headers,omitempty"` // basic
	Params  []AuthParam  `yaml:"params,omitempty"`  // basic

	// aws
	AccessKeyID     string `yaml:"access_key_id,omitempty"`
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	AssumeRoleARN   string `yaml:"assume_role_arn,omitempty"`
	RoleSessionName string `yaml:"role_session_name,omitempty"`
	STSEndpointURL  string `yaml:"sts_endpoint_url,omitempty"`
	BatchRoleARN    string `yaml:"batch_role_arn,omitempty"`

	// azure
	ClientID           string `yaml:"client_id,omitempty"`
	ClientSecret       string `yaml:"client_secret,omitempty"` //nolint:gosec
	TenantID           string `yaml:"tenant_id,omitempty"`
	UseManagedIdentity *bool  `yaml:"use_managed_identity,omitempty"`

	// gcp
	ServiceAccountJSON   string `yaml:"service_account_json,omitempty"`
	MetadataURL          string `yaml:"metadata_url,omitempty"`
	OAuthTokenURL        string `yaml:"oauth_token_url,omitempty"`
	UseGCPServiceAccount *bool  `yaml:"use_gcp_service_account,omitempty"`
}

// isEmpty reports whether no auth was decoded (used to detect the flattened form).
func (a ProviderAuth) isEmpty() bool {
	return a.Type == "" && len(a.Headers) == 0 && len(a.Params) == 0 &&
		a.AccessKeyID == "" && a.ServiceAccountJSON == "" &&
		a.ClientID == "" && a.UseManagedIdentity == nil && a.UseGCPServiceAccount == nil
}

// AuthHeader is a single auth header (maxItems 1 in the schema).
type AuthHeader struct {
	Name  string `yaml:"name,omitempty"`
	Value string `yaml:"value,omitempty"`
}

// AuthParam is a single auth query/body param (maxItems 1 in the schema).
type AuthParam struct {
	Name     string `yaml:"name,omitempty"`
	Value    string `yaml:"value,omitempty"`
	Location string `yaml:"location,omitempty"` // body | query
}
