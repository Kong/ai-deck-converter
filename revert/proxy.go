package revert

import "github.com/Kong/ai-deck-converter/internal/aigw"

// proxyFromConfig lifts the flat proxy_config record shared by the AI plugins
// (ai-proxy-advanced, ai-mcp-proxy) back into the structured AI Gateway proxy
// block. Reverses convert's proxyConfigBlock.
func proxyFromConfig(cfg map[string]any) *aigw.ProxyConfig {
	if len(cfg) == 0 {
		return nil
	}
	p := &aigw.ProxyConfig{
		ProxyScheme: getStr(cfg, "proxy_scheme"),
		NoProxy:     getStr(cfg, "no_proxy"),
	}
	if host, port := getStr(cfg, "http_proxy_host"), getInt(cfg, "http_proxy_port"); host != "" || port != nil {
		p.HTTPProxy = &aigw.ProxyHost{Host: host, Port: port}
	}
	if host, port := getStr(cfg, "https_proxy_host"), getInt(cfg, "https_proxy_port"); host != "" || port != nil {
		p.HTTPSProxy = &aigw.ProxyHost{Host: host, Port: port}
	}
	if user, pass := getStr(cfg, "auth_username"), getStr(cfg, "auth_password"); user != "" || pass != "" {
		p.Auth = &aigw.ProxyAuth{Username: user, Password: pass}
	}
	return p
}
