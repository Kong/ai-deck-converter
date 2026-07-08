package convert

// buildRegistries indexes source entities by name for cross-reference resolution.
func (c *Converter) buildRegistries() {
	for i := range c.src.Providers {
		p := &c.src.Providers[i]
		c.providers[p.Name] = p
	}
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		c.policies[p.Name] = p
	}
	for i := range c.src.IdentityProviders {
		p := &c.src.IdentityProviders[i]
		c.identityProviders[p.Name] = p
	}
	for i := range c.src.ConsumerGroups {
		g := &c.src.ConsumerGroups[i]
		c.consumerGroups[g.Name] = g
	}
}
