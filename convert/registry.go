package convert

import "github.com/Kong/ai-deck-converter/internal/aimap"

// buildRegistries indexes source entities by name for cross-reference resolution.
func (c *Converter) buildRegistries() {
	for i := range c.src.ModelProviders {
		p := &c.src.ModelProviders[i]
		if p.Type == aimap.KongStargateProvider {
			src := issueSource("model_provider", p.Name)
			if p.Config.Region != aimap.KongStargateLaunchRegion {
				c.reportError(src.at("config.region"), ConfigFieldInvalid, "Stargate region must be us")
			}
			if p.Config.HasAuth() {
				c.reportError(src.at("config.auth"), ConfigFieldInvalid, "Stargate uses managed authentication")
			}
			if p.TokenVault != nil || len(p.Datastores) > 0 {
				c.reportError(src.at("token_vault"), ConfigFieldInvalid, "Stargate uses managed authentication")
			}
		}
		c.providers[p.Name] = p
	}
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		c.policies[p.Name] = p
	}
	for i := range c.src.AuthStrategies {
		p := &c.src.AuthStrategies[i]
		c.authStrategies[p.Name] = p
	}
	for i := range c.src.ConsumerGroups {
		g := &c.src.ConsumerGroups[i]
		c.consumerGroups[g.Name] = g
	}
	for i := range c.src.Datastores {
		d := &c.src.Datastores[i]
		c.datastores[d.Name] = d
	}
}
