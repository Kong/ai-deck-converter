package convert

import "github.com/Kong/ai-deck-converter/internal/aigw"

// proxyConfigBlock lowers the structured AI Gateway proxy block into the flat
// proxy_config record shared by the AI plugins (ai-proxy-advanced,
// ai-mcp-proxy). Reversed by revert's proxyFromConfig.
func proxyConfigBlock(p *aigw.ProxyConfig) map[string]any {
	if p == nil {
		return nil
	}
	cfg := map[string]any{}
	if p.HTTPProxy != nil {
		setIfNotEmpty(cfg, "http_proxy_host", p.HTTPProxy.Host)
		if p.HTTPProxy.Port != nil {
			cfg["http_proxy_port"] = *p.HTTPProxy.Port
		}
	}
	if p.HTTPSProxy != nil {
		setIfNotEmpty(cfg, "https_proxy_host", p.HTTPSProxy.Host)
		if p.HTTPSProxy.Port != nil {
			cfg["https_proxy_port"] = *p.HTTPSProxy.Port
		}
	}
	setIfNotEmpty(cfg, "proxy_scheme", p.ProxyScheme)
	if p.Auth != nil {
		setIfNotEmpty(cfg, "auth_username", p.Auth.Username)
		setIfNotEmpty(cfg, "auth_password", p.Auth.Password)
	}
	setIfNotEmpty(cfg, "no_proxy", p.NoProxy)
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}
