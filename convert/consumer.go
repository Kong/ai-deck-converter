package convert

import (
	"fmt"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

func (c *Converter) convertConsumerGroups() {
	for i := range c.src.ConsumerGroups {
		g := &c.src.ConsumerGroups[i]
		src := issueSource(kindConsumerGroup, g.Name)
		c.convertEntity(src, func() error {
			plugins, err := c.scopedPlugins(src, g.Policies, aigw.ACLs{})
			if err != nil {
				return err
			}
			c.out.ConsumerGroups = append(c.out.ConsumerGroups, kong.ConsumerGroup{
				ID:      g.ID,
				Name:    g.Name,
				Plugins: plugins,
				Tags:    c.labelsToTags(g.Labels),
			})
			return nil
		})
	}
}

func (c *Converter) convertConsumers() {
	for i := range c.src.Consumers {
		cons := &c.src.Consumers[i]
		src := issueSource(kindConsumer, cons.Name)
		c.convertEntity(src, func() error {
			return c.convertConsumer(src, cons)
		})
	}
}

func (c *Converter) convertConsumer(src ConversionIssueSource, cons *aigw.Consumer) error {
	plugins, err := c.scopedPlugins(src, cons.Policies, aigw.ACLs{})
	if err != nil {
		return err
	}
	groups := make([]*kong.Ref, len(cons.ConsumerGroups))
	for j, g := range cons.ConsumerGroups {
		groups[j] = kong.NewRef(g)
	}
	kc := kong.Consumer{
		ID:       cons.ID,
		Username: cons.Name,
		CustomID: cons.CustomID,
		Groups:   groups,
		Plugins:  plugins,
		Tags:     c.labelsToTags(cons.Labels),
	}
	for j := range cons.Credentials {
		cred := &cons.Credentials[j]
		if cred.Type != "" && cred.Type != "api-key" {
			c.warn(src.at(fmt.Sprintf("credentials[%d].type", j)), ConfigFieldIgnored,
				"consumer %q credential %q has unsupported type %q; only api-key (keyauth) is supported",
				cons.Name, cred.Name, cred.Type)
			continue
		}
		kc.KeyAuthCredentials = append(kc.KeyAuthCredentials, kong.KeyAuthCredential{
			ID:   cred.ID,
			Key:  cred.APIKey,
			TTL:  cred.TTL,
			Tags: c.labelsToTags(cred.Labels),
		})
	}
	c.out.Consumers = append(c.out.Consumers, kc)
	return nil
}
