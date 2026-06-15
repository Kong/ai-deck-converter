package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

func (c *Converter) convertConsumerGroups() error {
	for i := range c.src.ConsumerGroups {
		g := &c.src.ConsumerGroups[i]
		plugins, err := c.scopedPlugins(g.Policies, aigw.ACLs{})
		if err != nil {
			return err
		}
		c.out.ConsumerGroups = append(c.out.ConsumerGroups, kong.ConsumerGroup{
			ID:      g.ID,
			Name:    g.Name,
			Plugins: plugins,
			Tags:    c.labelsToTags(g.Labels),
		})
	}
	return nil
}

func (c *Converter) convertConsumers() error {
	for i := range c.src.Consumers {
		cons := &c.src.Consumers[i]
		plugins, err := c.scopedPlugins(cons.Policies, aigw.ACLs{})
		if err != nil {
			return err
		}
		kc := kong.Consumer{
			ID:       cons.ID,
			Username: cons.Name,
			CustomID: cons.CustomID,
			Groups:   cons.ConsumerGroups,
			Plugins:  plugins,
			Tags:     c.labelsToTags(cons.Labels),
		}
		for j := range cons.Credentials {
			cred := &cons.Credentials[j]
			if cred.Type != "" && cred.Type != "api-key" {
				if err := c.warn("consumer %q credential %q has unsupported type %q; only api-key (keyauth) is supported", cons.Name, cred.Name, cred.Type); err != nil {
					return err
				}
				continue
			}
			kc.KeyAuthCredentials = append(kc.KeyAuthCredentials, kong.KeyAuthCredential{
				ID:  cred.ID,
				Key: cred.APIKey,
				TTL: cred.TTL,
			})
		}
		c.out.Consumers = append(c.out.Consumers, kc)
	}
	return nil
}
