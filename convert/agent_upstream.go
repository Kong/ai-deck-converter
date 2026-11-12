package convert

import (
	"fmt"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// agentUpstream lowers config.targets and config.balancer into a Kong
// Upstream named <agent>-upstream. The agent Service uses it as its host.
func (c *Converter) agentUpstream(src ConversionIssueSource, a *aigw.Agent) (kong.Upstream, error) {
	if !c.supports(aimap.FeatureAgentTargets) {
		return kong.Upstream{}, c.failUnsupported(src.at("config.targets"), aimap.FeatureAgentTargets, "config.targets")
	}
	up := kong.Upstream{Name: a.Name + "-upstream", Tags: c.labelsToTags(a.Labels)}

	for i, t := range a.Config.Targets {
		if t.Address == "" {
			c.warn(src.at(fmt.Sprintf("config.targets[%d].address", i)), MissingRequiredConfigField,
				"agent %q target %d has no address; target left out", a.Name, i)
			continue
		}
		up.Targets = append(up.Targets, kong.UpstreamTarget{Target: t.Address, Weight: t.Weight, Failover: t.Failover})
	}
	if len(up.Targets) == 0 {
		return kong.Upstream{}, c.fail(src.at("config.targets"), MissingRequiredConfigField,
			"agent %q has no usable config.targets; the Upstream needs a target", a.Name)
	}

	if err := c.applyUpstreamBalancer(src, a, &up); err != nil {
		return kong.Upstream{}, err
	}
	return up, nil
}

func (c *Converter) applyUpstreamBalancer(src ConversionIssueSource, a *aigw.Agent, up *kong.Upstream) error {
	b := a.Config.Balancer
	if b == nil || b.Algorithm == "" {
		up.Algorithm = aimap.UpstreamAlgorithms["round-robin"]
		return nil
	}
	algorithm, ok := aimap.UpstreamAlgorithms[b.Algorithm]
	if !ok {
		return c.fail(src.at("config.balancer.algorithm"), ConfigFieldInvalid,
			"agent %q: unsupported balancer algorithm %q", a.Name, b.Algorithm)
	}
	up.Algorithm = algorithm
	if b.Algorithm == "consistent-hashing" {
		header, _ := b.Fields["hash_on_header"].(string)
		if header == "" {
			header = aimap.DefaultUpstreamHashHeader
		}
		up.HashOn, up.HashOnHeader = "header", header
	}
	return nil
}
