package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// convertAgents translates AI Gateway agents into a Gateway Service + Route.
// "a2a" agents additionally get an ai-a2a-proxy plugin; "http" agents are plain
// proxies.
func (c *Converter) convertAgents() error {
	for i := range c.src.Agents {
		a := &c.src.Agents[i]
		route := buildRoute(a.Config.Route, a.Name)

		guard, err := c.scopedPlugins(a.Policies, a.ACLs)
		if err != nil {
			return err
		}
		route.Plugins = append(route.Plugins, guard...)

		switch a.Type {
		case "a2a":
			route.Plugins = append(route.Plugins, a2aPlugin(a))
		case "http":
			// plain HTTP proxy: Service + Route only
		default:
			if err := c.warn("agent %q has unknown type %q; treating as plain http proxy", a.Name, a.Type); err != nil {
				return err
			}
		}

		if a.Config.URL == "" {
			if err := c.warn("agent %q has no config.url; the Service will be missing an upstream", a.Name); err != nil {
				return err
			}
		}
		c.out.Services = append(c.out.Services, kong.Service{
			Name:   a.Name,
			URL:    a.Config.URL,
			Routes: []kong.Route{route},
			Tags:   c.labelsToTags(a.Labels),
		})
	}
	return nil
}

func a2aPlugin(a *aigw.Agent) kong.Plugin {
	cfg := map[string]any{}
	if logging := loggingBlock(a.Config.Logging); logging != nil {
		// log_audits is an ai-mcp-proxy field; the ai-a2a-proxy schema has no
		// such key, so drop it to avoid emitting an unknown field.
		delete(logging, "log_audits")
		if len(logging) > 0 {
			cfg["logging"] = logging
		}
	}
	if a.Config.MaxRequestBodySize != nil {
		cfg["max_request_body_size"] = *a.Config.MaxRequestBodySize
	}
	return kong.Plugin{Name: "ai-a2a-proxy", Config: cfg}
}

// loggingBlock maps AI Gateway logging to the {log_statistics, log_payloads,
// max_payload_size} block used by the ai-a2a-proxy and ai-mcp-proxy plugins.
func loggingBlock(l *aigw.Logging) map[string]any {
	if l == nil {
		return nil
	}
	block := map[string]any{}
	if l.Statistics != nil {
		block["log_statistics"] = *l.Statistics
	}
	if l.Payloads != nil {
		block["log_payloads"] = *l.Payloads
	}
	if l.MaxPayloadSize != nil {
		block["max_payload_size"] = *l.MaxPayloadSize
	}
	if l.Audits != nil {
		block["log_audits"] = *l.Audits
	}
	if len(block) == 0 {
		return nil
	}
	return block
}
