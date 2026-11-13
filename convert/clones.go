package convert

import (
	"fmt"
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// postAIClone is the clone a model policy needs to run after ai-proxy-advanced.
type postAIClone struct {
	name string
	base string
}

// planResponseTransformerClones reports, per plugin index, the clone that moves
// a model-scoped response transformer after ai-proxy-advanced.
//
// ai-proxy-advanced writes the response body in body_filter at 770. Dynamic
// ordering applies to the access phase only, so priority 800 runs before it and
// the body change is lost. A clone carries its own priority, which applies to
// every phase.
//
// A headers-only transformer keeps its static priority, because its header
// change already survives. Only a type "model" body model is cloned, because
// ai-proxy-advanced sets the response body only when it normalized one.
//
// An issue names the model and the policy's position, because decK carries no
// policy name and revert cannot recover it.
func (c *Converter) planResponseTransformerClones(
	src ConversionIssueSource, policies []string, plugins []kong.Plugin, proxyRewritesBody bool,
) map[int]postAIClone {
	clones := map[int]postAIClone{}
	if !proxyRewritesBody {
		return clones
	}
	for i := range plugins {
		p := plugins[i]
		cloneName, ok := aimap.PostAICloneName(p.Name)
		if !ok || !transformsResponseBody(p.Config) {
			continue
		}
		field := scopedPluginField(policies, p)
		if !c.supports(aimap.FeatureClonedPlugins) {
			c.warn(src.at(field), BehaviorMayDiffer,
				"a %q policy's response body change is lost, because ai-proxy-advanced writes the body "+
					"after it. Reordering a cloned plugin needs minimum runtime version %q, but the "+
					"configured minimum is %q. Only the response headers change.",
				p.Name, aimap.Since(aimap.FeatureClonedPlugins), c.minRuntimeVersion)
			continue
		}
		if p.Name == "response-transformer" {
			c.warn(src.at(field), BehaviorMayDiffer,
				"a %q policy does not change the response body of the model for a client that accepts "+
					"gzip. response-transformer does not decode the body ai-proxy-advanced compressed. "+
					"Use response-transformer-advanced to transform compressed responses too.",
				p.Name)
		}
		clones[i] = postAIClone{name: cloneName, base: p.Name}
	}
	return clones
}

// applyPostAIClone returns p under its clone name, and the clone definition that
// rename needs. A WebSocket route keeps the original name, because the drop
// warning must name the plugin the user wrote.
func applyPostAIClone(p kong.Plugin, clone postAIClone, websocket bool) (kong.Plugin, *kong.ClonedPlugin) {
	if websocket {
		return p, nil
	}
	priority := aimap.ResponseTransformerClonePriority
	p.Name = clone.name
	return p, &kong.ClonedPlugin{Name: clone.name, Ref: clone.base, Priority: &priority}
}

// warnUnreorderedResponseTransformers reports the response transformers the
// converter cannot move below ai-proxy-advanced. A global, consumer- or
// consumer-group-scoped plugin is one instance for all of its routes, so moving
// it would also reorder routes that run no ai-proxy-advanced.
func (c *Converter) warnUnreorderedResponseTransformers() {
	if !slices.ContainsFunc(c.out.Plugins, func(p kong.Plugin) bool {
		return p.Name == "ai-proxy-advanced"
	}) {
		return
	}
	for i := range c.src.Policies {
		p := &c.src.Policies[i]
		if _, ok := aimap.PostAICloneName(p.Type); !ok || !transformsResponseBody(p.Config) {
			continue
		}
		if p.Global != nil && *p.Global {
			c.warn(issueSource(kindPolicy, p.Name).at("global"), BehaviorMayDiffer,
				"global policy %q (type %q) cannot change the response body of a model: "+
					"ai-proxy-advanced writes the body after it, and the converter does not reorder a "+
					"global plugin. Attach the policy to the model instead.",
				p.Name, p.Type)
			continue
		}
		c.warnScopedResponseTransformer(p)
	}
}

// warnScopedResponseTransformer reports policy p when a consumer or consumer
// group references it. Those plugins scope to the consumer, not to a model, so
// the clone cannot reorder them either.
func (c *Converter) warnScopedResponseTransformer(p *aigw.Policy) {
	for i := range c.src.Consumers {
		cons := &c.src.Consumers[i]
		field := policyReferenceField(cons.Policies, p.Name)
		if field == "" {
			continue
		}
		c.warn(issueSource(kindConsumer, cons.Name).at(field), BehaviorMayDiffer,
			"policy %q (type %q) on consumer %q cannot change the response body of a model: "+
				"ai-proxy-advanced writes the body after it, and the converter does not reorder a "+
				"consumer-scoped plugin. Attach the policy to the model instead.",
			p.Name, p.Type, cons.Name)
	}
	for i := range c.src.ConsumerGroups {
		group := &c.src.ConsumerGroups[i]
		field := policyReferenceField(group.Policies, p.Name)
		if field == "" {
			continue
		}
		c.warn(issueSource(kindConsumerGroup, group.Name).at(field), BehaviorMayDiffer,
			"policy %q (type %q) on consumer group %q cannot change the response body of a model: "+
				"ai-proxy-advanced writes the body after it, and the converter does not reorder a "+
				"consumer-group-scoped plugin. Attach the policy to the model instead.",
			p.Name, p.Type, group.Name)
	}
}

// policyReferenceField returns the field path of the reference to name in refs,
// or "" when refs does not name it.
func policyReferenceField(refs []string, name string) string {
	if i := slices.Index(refs, name); i >= 0 {
		return fmt.Sprintf("policies[%d]", i)
	}
	return ""
}

// transformsResponseBody reports whether a response transformer config changes
// the response body. It covers every field the two plugins act on.
func transformsResponseBody(config map[string]any) bool {
	// rename.json exists only in response-transformer; the advanced schema has
	// no such field. Both act on add, remove, replace and append.
	for _, block := range []string{"add", "remove", "replace", "append", "rename"} {
		if hasEntries(config, block, "json") {
			return true
		}
	}
	if hasEntries(config, "allow", "json") {
		return true
	}
	if replace, ok := config["replace"].(map[string]any); ok {
		if body, ok := replace["body"].(string); ok && body != "" {
			return true
		}
	}
	// transform.json only scopes transform.functions, so it changes nothing on
	// its own.
	transform, ok := config["transform"].(map[string]any)
	if !ok {
		return false
	}
	return hasEntries(transform, "functions")
}

// hasEntries reports whether config holds a non-empty list at path.
func hasEntries(config map[string]any, path ...string) bool {
	var current any = config
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current = m[key]
	}
	entries, ok := current.([]any)
	return ok && len(entries) > 0
}
