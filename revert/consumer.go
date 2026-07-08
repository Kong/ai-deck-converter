package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// anonymousConsumerName mirrors convert.anonymousConsumerName: the
// username/custom_id of the synthesized anonymous-fallback Consumer.
const anonymousConsumerName = "anonymous"

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
		if isSynthesizedAnonymousConsumer(c) {
			// Converter-generated identity-provider infrastructure, not
			// user-declared state; recreated automatically on the next convert.
			continue
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

// isSynthesizedAnonymousConsumer reports whether c matches exactly what
// convert.ensureAnonymousConsumer synthesizes: the "anonymous" consumer with
// a single request-termination(401, "Unauthorized") plugin, no credentials or
// groups. A hand-written "anonymous" consumer with any other shape is
// reverted normally.
func isSynthesizedAnonymousConsumer(c *kong.Consumer) bool {
	if c.Username != anonymousConsumerName || c.CustomID != anonymousConsumerName {
		return false
	}
	if len(c.Groups) > 0 || len(c.KeyAuthCredentials) > 0 {
		return false
	}
	if len(c.Plugins) != 1 {
		return false
	}
	p := c.Plugins[0]
	if p.Name != "request-termination" {
		return false
	}
	code, _ := p.Config["status_code"]
	msg, _ := p.Config["message"]
	statusOK := code == 401 || code == float64(401)
	return statusOK && msg == "Unauthorized" && len(p.Config) == 2
}
