package convert

import (
	"fmt"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

const defaultAgentTargetsProtocol = "https"

// The default health checks of an agent Upstream. TCP probes need no path,
// so they work for each agent type. Passive checks count only TCP failures
// and timeouts. An application error does not mark a target unhealthy.
const (
	agentHealthcheckInterval    = 10.0
	agentHealthcheckSuccesses   = 2
	agentHealthcheckTCPFailures = 3
	agentHealthcheckTimeouts    = 3
)

// checkAgentURLOnlyFields returns an error if an agent without targets sets a
// field that only targets use.
func (c *Converter) checkAgentURLOnlyFields(src ConversionIssueSource, a *aigw.Agent) error {
	fields := []struct {
		name string
		set  bool
	}{
		{"config.balancer", a.Config.Balancer != nil},
		{"config.protocol", a.Config.Protocol != ""},
		{"config.path", a.Config.Path != ""},
	}
	for _, f := range fields {
		if f.set {
			return c.fail(src.at(f.name), ConfigFieldInvalid,
				"agent %q sets %s, which requires config.targets", a.Name, f.name)
		}
	}
	return nil
}

// applyAgentTargets lowers config.targets into a Kong Upstream named
// <agent>-upstream and points the Service at it.
func (c *Converter) applyAgentTargets(src ConversionIssueSource, a *aigw.Agent, svc *kong.Service) error {
	if !c.supports(aimap.FeatureAgentTargets) {
		return c.failUnsupported(src.at("config.targets"), aimap.FeatureAgentTargets, "config.targets")
	}
	protocol, err := c.agentTargetsProtocol(src, a)
	if err != nil {
		return err
	}
	if a.Config.Path != "" && !strings.HasPrefix(a.Config.Path, "/") {
		return c.fail(src.at("config.path"), ConfigFieldInvalid,
			"agent %q config.path %q must start with /", a.Name, a.Config.Path)
	}

	up := kong.Upstream{
		Name:         a.Name + "-upstream",
		Healthchecks: defaultAgentHealthchecks(),
		Tags:         c.labelsToTags(a.Labels),
		Source:       agentUpstreamSource(a.Name),
	}
	if err := c.addAgentTargets(src, a, &up); err != nil {
		return err
	}
	if err := c.applyUpstreamBalancer(src, a, &up); err != nil {
		return err
	}
	if err := c.checkAgentFailover(src, a, up.Algorithm); err != nil {
		return err
	}

	svc.URL, svc.Host, svc.Protocol, svc.Path = "", up.Name, protocol, a.Config.Path
	svc.Source = agentTargetsServiceSource(a.Name)
	c.out.Upstreams = append(c.out.Upstreams, up)
	return nil
}

func (c *Converter) agentTargetsProtocol(src ConversionIssueSource, a *aigw.Agent) (string, error) {
	switch a.Config.Protocol {
	case "":
		return defaultAgentTargetsProtocol, nil
	case "http", "https":
		return a.Config.Protocol, nil
	default:
		return "", c.fail(src.at("config.protocol"), ConfigFieldInvalid,
			"agent %q has unsupported config.protocol %q; use http or https", a.Name, a.Config.Protocol)
	}
}

func (c *Converter) addAgentTargets(src ConversionIssueSource, a *aigw.Agent, up *kong.Upstream) error {
	firstIndex := map[string]int{}
	hasRegularTarget := false
	for i, t := range a.Config.Targets {
		field := fmt.Sprintf("config.targets[%d]", i)
		if t.Address == "" {
			return c.fail(src.at(field+".address"), MissingRequiredConfigField,
				"agent %q target %d has no address", a.Name, i)
		}
		// Targets get IDs from their address. A repeated address gives a repeated ID.
		if j, ok := firstIndex[t.Address]; ok {
			return c.fail(src.at(field+".address"), ConfigFieldInvalid,
				"agent %q target %d repeats the address %q of target %d", a.Name, i, t.Address, j)
		}
		firstIndex[t.Address] = i
		if t.Failover == nil || !*t.Failover {
			hasRegularTarget = true
		}
		up.Targets = append(up.Targets, kong.UpstreamTarget{
			Target: t.Address, Weight: t.Weight, Failover: t.Failover,
			Source: source("agent", a.Name, field,
				kong.FieldMapping{GeneratedPrefix: "target", SourcePrefix: field + ".address"}),
		})
	}
	if !hasRegularTarget {
		return c.fail(src.at("config.targets"), ConfigFieldInvalid,
			"agent %q has only failover targets; at least one target must not be a failover target", a.Name)
	}
	return nil
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
		// The gateway sets no header on agent traffic that can be a default.
		if b.HashOnHeader == "" {
			return c.fail(src.at("config.balancer.hash_on_header"), MissingRequiredConfigField,
				"agent %q uses consistent-hashing without config.balancer.hash_on_header", a.Name)
		}
		up.HashOn, up.HashOnHeader = "header", b.HashOnHeader
	}
	return nil
}

// checkAgentFailover returns an error for a failover target that the
// algorithm does not support.
func (c *Converter) checkAgentFailover(src ConversionIssueSource, a *aigw.Agent, kongAlgorithm string) error {
	// Kong leaves failover targets out of the consistent-hashing ring.
	if kongAlgorithm != aimap.UpstreamAlgorithms["consistent-hashing"] {
		return nil
	}
	for i, t := range a.Config.Targets {
		if t.Failover != nil && *t.Failover {
			return c.fail(src.at(fmt.Sprintf("config.targets[%d].failover", i)), ConfigFieldInvalid,
				"agent %q target %d is a failover target, which consistent-hashing does not support", a.Name, i)
		}
	}
	return nil
}

// defaultAgentHealthchecks returns active and passive checks that mark a
// target unhealthy and healthy again.
func defaultAgentHealthchecks() *kong.Healthchecks {
	threshold := func(t kong.HealthcheckThreshold) *kong.HealthcheckThreshold { return &t }
	return &kong.Healthchecks{
		Active: &kong.ActiveHealthcheck{
			Type: "tcp",
			Healthy: threshold(kong.HealthcheckThreshold{
				Interval:  floatPtr(agentHealthcheckInterval),
				Successes: intPtr(agentHealthcheckSuccesses),
			}),
			Unhealthy: threshold(kong.HealthcheckThreshold{
				Interval:    floatPtr(agentHealthcheckInterval),
				TCPFailures: intPtr(agentHealthcheckTCPFailures),
				Timeouts:    intPtr(agentHealthcheckTimeouts),
			}),
		},
		Passive: &kong.PassiveHealthcheck{
			Type: "tcp",
			Unhealthy: threshold(kong.HealthcheckThreshold{
				TCPFailures: intPtr(agentHealthcheckTCPFailures),
				Timeouts:    intPtr(agentHealthcheckTimeouts),
			}),
		},
	}
}

func floatPtr(f float64) *float64 { return &f }
