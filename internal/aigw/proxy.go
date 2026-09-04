package aigw

// ProxyConfig mirrors the AIGatewayProxyConfig schema: the outbound
// HTTP/HTTPS proxy configuration for requests to the upstream, carried by
// Models (config.proxy) and MCP Servers (config.proxy). It lowers to the flat
// proxy_config record shared by the AI plugins.
type ProxyConfig struct {
	HTTPProxy   *ProxyHost `yaml:"http_proxy,omitempty"`
	HTTPSProxy  *ProxyHost `yaml:"https_proxy,omitempty"`
	ProxyScheme string     `yaml:"proxy_scheme,omitempty"`
	Auth        *ProxyAuth `yaml:"auth,omitempty"`
	NoProxy     string     `yaml:"no_proxy,omitempty"`
	// AIGW21Demo is a deliberately optional compatibility-spike field. It
	// verifies that fields configured on native AI entities can be carried to
	// the generated AI plugins without an API-side direct plugin config.
	AIGW21Demo *bool `yaml:"aigw_2_1_demo,omitempty"`
}

// ProxyHost is one proxy server address (host + port).
type ProxyHost struct {
	Host string `yaml:"host,omitempty"`
	Port *int   `yaml:"port,omitempty"`
}

// ProxyAuth is the credential pair used to authenticate to the proxy server.
type ProxyAuth struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}
