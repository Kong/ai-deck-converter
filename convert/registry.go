package convert

import "github.com/Kong/ai-deck-converter/internal/aimap"

// buildRegistries indexes source entities by name for cross-reference resolution.
func (c *Converter) buildRegistries() {
	for i := range c.src.ModelProviders {
		p := &c.src.ModelProviders[i]
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
	c.resolveAuthStrategyRedis()
}

// resolveAuthStrategyRedis puts the redis-ee Datastore of each auth strategy
// in its config. The registry holds a copy, so the source stays.
func (c *Converter) resolveAuthStrategyRedis() {
	for name, idp := range c.authStrategies {
		ds := c.authStrategyRedisDatastore(idp)
		if ds == nil {
			continue
		}
		resolved := *idp
		if idp.Type == authStrategyTypeOpenIDConnect {
			resolved.Config = aimap.ApplyPrincipalsCacheRedisTopLevel(idp.Config, ds)
		} else {
			resolved.Config = aimap.ApplyPrincipalsCacheRedis(idp.Config, ds)
		}
		c.authStrategies[name] = &resolved
	}
}
