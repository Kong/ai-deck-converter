package revert

import (
	"github.com/gperanich/ai-deck-converter/internal/aigw"
	"github.com/gperanich/ai-deck-converter/internal/kong"
)

func (r *Reverter) revertConsumerGroups() error {
	for i := range r.src.ConsumerGroups {
		g := &r.src.ConsumerGroups[i]
		plugins := append(append([]kong.Plugin{}, g.Plugins...), r.idx.group[g.Name]...)
		refs, acls := r.policyRefs(plugins)
		if !acls.IsEmpty() {
			if err := r.warn("acl plugin on consumer group %q has no AI Gateway representation; dropped", g.Name); err != nil {
				return err
			}
		}
		r.out.ConsumerGroups = append(r.out.ConsumerGroups, aigw.ConsumerGroup{
			Name:     g.Name,
			Policies: refs,
			Labels:   r.tagsToLabels(g.Tags),
		})
	}
	return nil
}

func (r *Reverter) revertConsumers() error {
	for i := range r.src.Consumers {
		c := &r.src.Consumers[i]
		name := c.Username
		if name == "" {
			name = c.CustomID
		}
		plugins := append(append([]kong.Plugin{}, c.Plugins...), r.idx.consumer[c.Username]...)
		refs, acls := r.policyRefs(plugins)
		if !acls.IsEmpty() {
			if err := r.warn("acl plugin on consumer %q has no AI Gateway representation; dropped", name); err != nil {
				return err
			}
		}
		ac := aigw.Consumer{
			Name:           name,
			CustomID:       c.CustomID,
			ConsumerGroups: c.Groups,
			Policies:       refs,
			Labels:         r.tagsToLabels(c.Tags),
		}
		for j := range c.KeyAuthCredentials {
			cred := &c.KeyAuthCredentials[j]
			ac.Credentials = append(ac.Credentials, aigw.Credential{
				Type:   "api-key",
				APIKey: cred.Key,
				TTL:    cred.TTL,
			})
		}
		if len(ac.Credentials) > 0 {
			ac.Type = "api-key"
		}
		r.out.Consumers = append(r.out.Consumers, ac)
	}
	return nil
}
