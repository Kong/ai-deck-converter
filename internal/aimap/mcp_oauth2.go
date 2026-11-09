package aimap

// OAuth2FieldKind tells how revert changes an ai-mcp-oauth2 plugin config
// value into an openid-connect auth strategy config value.
type OAuth2FieldKind int

const (
	// OAuth2Passthrough copies the value verbatim in both directions. The
	// OIDC and plugin fields share the same shape and semantics.
	OAuth2Passthrough OAuth2FieldKind = iota
	// OAuth2FirstOfArray lowers an OIDC array-of-scalars into a single plugin
	// scalar (element 0) and lifts the scalar back into a one-element array.
	OAuth2FirstOfArray
	// OAuth2FirstOfPaths lowers an OIDC array-of-paths ([][]string) into a
	// single plugin path ([]string, element 0) and lifts it back into a
	// one-element array-of-paths.
	OAuth2FirstOfPaths
)

// OAuth2FieldMap maps one openid-connect auth-strategy config key onto one
// ai-mcp-oauth2 plugin config key. OIDCKey and OAuth2Key are equal except for
// the deliberately renamed fields (e.g. leeway -> jwt_claims_leeway).
type OAuth2FieldMap struct {
	OIDCKey   string
	OAuth2Key string
	Kind      OAuth2FieldKind
}

// OIDCToMCPOAuth2Fields lists the ai-mcp-oauth2 fields that revert lifts onto
// an openid-connect auth strategy. Convert never lowers a strategy onto
// ai-mcp-oauth2.
//
// client_id, client_secret, ssl_verify are handled here too (client_id /
// client_secret are FirstOfArray; ssl_verify is Passthrough). The
// issuer -> authorization_servers and scopes -> scopes_supported fallbacks are
// NOT in this table: they are attributed to access.metadata rather than to the
// auth strategy, so convert/revert keep handling them separately.
//
// Deliberately excluded as not cleanly convertible: token_exchange (false
// friend — OIDC legacy grant vs oauth2 RFC-8693 upstream-exchange object),
// client_jwk (OIDC JWK object array vs oauth2 serialized string),
// introspection_endpoint_auth_method (would collide with client_auth as a
// second source), the downstream header-mapping fields (structured + fuzzy
// semantics), tls_client_auth_cert_id (cert-entity UUID vs inline PEM),
// and extra_jwks_uris (no oauth2 target).
var OIDCToMCPOAuth2Fields = []OAuth2FieldMap{
	// Client authentication credentials / parameters.
	{OIDCKey: "client_id", OAuth2Key: "client_id", Kind: OAuth2FirstOfArray},
	{OIDCKey: "client_secret", OAuth2Key: "client_secret", Kind: OAuth2FirstOfArray},
	{OIDCKey: "client_alg", OAuth2Key: "client_alg", Kind: OAuth2FirstOfArray},
	{OIDCKey: "client_auth", OAuth2Key: "client_auth", Kind: OAuth2FirstOfArray},

	// Token verification.
	{OIDCKey: "introspection_endpoint", OAuth2Key: "introspection_endpoint", Kind: OAuth2Passthrough},
	{OIDCKey: "mtls_introspection_endpoint", OAuth2Key: "mtls_introspection_endpoint", Kind: OAuth2Passthrough},
	{OIDCKey: "cache_introspection", OAuth2Key: "cache_introspection", Kind: OAuth2Passthrough},
	{OIDCKey: "jwks_endpoint", OAuth2Key: "jwks_endpoint", Kind: OAuth2Passthrough},
	{OIDCKey: "leeway", OAuth2Key: "jwt_claims_leeway", Kind: OAuth2Passthrough},
	{OIDCKey: "ssl_verify", OAuth2Key: "ssl_verify", Kind: OAuth2Passthrough},

	// Consumer / credential mapping.
	{OIDCKey: "consumer_by", OAuth2Key: "consumer_by", Kind: OAuth2Passthrough},
	{OIDCKey: "consumer_claims", OAuth2Key: "consumer_claim", Kind: OAuth2FirstOfPaths},
	{OIDCKey: "consumer_optional", OAuth2Key: "consumer_optional", Kind: OAuth2Passthrough},
	{OIDCKey: "consumer_groups_claim", OAuth2Key: "consumer_groups_claim", Kind: OAuth2Passthrough},
	{OIDCKey: "consumer_groups_optional", OAuth2Key: "consumer_groups_optional", Kind: OAuth2Passthrough},
	{OIDCKey: "credential_claim", OAuth2Key: "credential_claim", Kind: OAuth2Passthrough},

	// Network.
	{OIDCKey: "keepalive", OAuth2Key: "keepalive", Kind: OAuth2Passthrough},
	{OIDCKey: "timeout", OAuth2Key: "timeout", Kind: OAuth2Passthrough},
	{OIDCKey: "http_version", OAuth2Key: "http_version", Kind: OAuth2Passthrough},
	{OIDCKey: "upstream_headers", OAuth2Key: "upstream_headers", Kind: OAuth2Passthrough},
}

// Revert lifts two further ai-mcp-oauth2 fields in
// revert/mcp_auth_strategy.go:
//
//   - passthrough_credentials: true becomes hide_credentials: false.
//
//   - insecure_relaxed_audience_validation: false becomes audience_required.
//     The value is expected_audience, or else the resource.
//
// The openid-connect lowering in mcpOIDCPlugin always writes auth_methods
// ["bearer"]. Revert leaves out exactly ["bearer"] from the strategy. It lifts
// any other list unchanged.
