package convert

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
}
