package convert

import (
	"fmt"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

const defaultTargetsProtocol = "https"

// The default health checks of a targets Upstream. TCP probes need no path,
// so they work for each agent and MCP server type. Passive checks count only
// TCP failures and timeouts. An application error does not mark a target
// unhealthy.
const (
	targetsHealthcheckInterval    = 10.0
	targetsHealthcheckSuccesses   = 2
	targetsHealthcheckTCPFailures = 3
	targetsHealthcheckTimeouts    = 3
)

// upstreamTargets holds the fields that point the Service of an agent or an
// MCP server at an Upstream.
type upstreamTargets struct {
	// entityType is the provenance kind. label names the entity in messages.
	entityType, label, name string
	feature                 aimap.Feature
	targets                 []aigw.UpstreamTarget
	balancer                *aigw.UpstreamBalancer
	protocol, path          string
}

func agentUpstreamTargets(a *aigw.Agent) upstreamTargets {
	return upstreamTargets{
		entityType: "agent", label: "agent", name: a.Name, feature: aimap.FeatureAgentTargets,
		targets: a.Config.Targets, balancer: a.Config.Balancer,
		protocol: a.Config.Protocol, path: a.Config.Path,
	}
}

func mcpUpstreamTargets(m *aigw.MCPServer) upstreamTargets {
	return upstreamTargets{
		entityType: "mcp_server", label: "MCP server", name: m.Name, feature: aimap.FeatureMCPTargets,
		targets: m.Config.Targets, balancer: m.Config.Balancer,
		protocol: m.Config.Protocol, path: m.Config.Path,
	}
}

// checkURLOnlyFields returns an error if an entity without targets sets a
// field that only targets use.
func (c *Converter) checkURLOnlyFields(src ConversionIssueSource, u upstreamTargets) error {
	fields := []struct {
		name string
		set  bool
	}{
		{"config.balancer", u.balancer != nil},
		{"config.protocol", u.protocol != ""},
		{"config.path", u.path != ""},
	}
	for _, f := range fields {
		if f.set {
			return c.fail(src.at(f.name), ConfigFieldInvalid,
				"%s %q sets %s, which requires config.targets", u.label, u.name, f.name)
		}
	}
	return nil
}

// applyTargets lowers config.targets into a Kong Upstream named
// <name>-upstream and points the Service at it.
func (c *Converter) applyTargets(src ConversionIssueSource, u upstreamTargets, svc *kong.Service) error {
	if !c.supports(u.feature) {
		return c.failUnsupported(src.at("config.targets"), u.feature, "config.targets")
	}
	protocol, err := c.targetsProtocol(src, u)
	if err != nil {
		return err
	}
	if u.path != "" && !strings.HasPrefix(u.path, "/") {
		return c.fail(src.at("config.path"), ConfigFieldInvalid,
			"%s %q config.path %q must start with /", u.label, u.name, u.path)
	}

	up := kong.Upstream{
		Name:         u.name + "-upstream",
		Healthchecks: defaultTargetsHealthchecks(),
		Tags:         svc.Tags,
		Source:       targetsUpstreamSource(u.entityType, u.name),
	}
	if err := c.addTargets(src, u, &up); err != nil {
		return err
	}
	if err := c.applyUpstreamBalancer(src, u, &up); err != nil {
		return err
	}
	if err := c.checkFailover(src, u, up.Algorithm); err != nil {
		return err
	}

	svc.URL, svc.Host, svc.Protocol, svc.Path = "", up.Name, protocol, u.path
	svc.Source = targetsServiceSource(u.entityType, u.name)
	c.out.Upstreams = append(c.out.Upstreams, up)
	return nil
}

func (c *Converter) targetsProtocol(src ConversionIssueSource, u upstreamTargets) (string, error) {
	switch u.protocol {
	case "":
		return defaultTargetsProtocol, nil
	case "http", "https":
		return u.protocol, nil
	default:
		return "", c.fail(src.at("config.protocol"), ConfigFieldInvalid,
			"%s %q has unsupported config.protocol %q; use http or https", u.label, u.name, u.protocol)
	}
}

func (c *Converter) addTargets(src ConversionIssueSource, u upstreamTargets, up *kong.Upstream) error {
	firstIndex := map[string]int{}
	hasRegularTarget := false
	for i, t := range u.targets {
		field := fmt.Sprintf("config.targets[%d]", i)
		if t.Address == "" {
			return c.fail(src.at(field+".address"), MissingRequiredConfigField,
				"%s %q target %d has no address", u.label, u.name, i)
		}
		// Targets get IDs from their address. A repeated address gives a repeated ID.
		if j, ok := firstIndex[t.Address]; ok {
			return c.fail(src.at(field+".address"), ConfigFieldInvalid,
				"%s %q target %d repeats the address %q of target %d", u.label, u.name, i, t.Address, j)
		}
		firstIndex[t.Address] = i
		if t.Failover == nil || !*t.Failover {
			hasRegularTarget = true
		}
		up.Targets = append(up.Targets, kong.UpstreamTarget{
			Target: t.Address, Weight: t.Weight, Failover: t.Failover,
			Source: source(u.entityType, u.name, field,
				kong.FieldMapping{GeneratedPrefix: "target", SourcePrefix: field + ".address"}),
		})
	}
	if !hasRegularTarget {
		return c.fail(src.at("config.targets"), ConfigFieldInvalid,
			"%s %q has only failover targets; at least one target must not be a failover target", u.label, u.name)
	}
	return nil
}

func (c *Converter) applyUpstreamBalancer(src ConversionIssueSource, u upstreamTargets, up *kong.Upstream) error {
	b := u.balancer
	if b == nil {
		up.Algorithm = aimap.UpstreamAlgorithms["round-robin"]
		return nil
	}
	name := b.Algorithm
	if name == "" {
		name = "round-robin"
	}
	algorithm, ok := aimap.UpstreamAlgorithms[name]
	if !ok {
		return c.fail(src.at("config.balancer.algorithm"), ConfigFieldInvalid,
			"%s %q: unsupported balancer algorithm %q", u.label, u.name, b.Algorithm)
	}
	up.Algorithm = algorithm
	if name != "consistent-hashing" && b.HashOnHeader != "" {
		c.warn(src.at("config.balancer.hash_on_header"), ConfigFieldIgnored,
			"%s %q sets config.balancer.hash_on_header, which only consistent-hashing uses", u.label, u.name)
	}
	if name == "consistent-hashing" {
		// The gateway sets no header on agent or MCP traffic that can be a default.
		if b.HashOnHeader == "" {
			return c.fail(src.at("config.balancer.hash_on_header"), MissingRequiredConfigField,
				"%s %q uses consistent-hashing without config.balancer.hash_on_header", u.label, u.name)
		}
		up.HashOn, up.HashOnHeader = "header", b.HashOnHeader
	}
	return nil
}

// checkFailover returns an error for a failover target that the algorithm
// does not support.
func (c *Converter) checkFailover(src ConversionIssueSource, u upstreamTargets, kongAlgorithm string) error {
	// Kong leaves failover targets out of the consistent-hashing ring.
	if kongAlgorithm != aimap.UpstreamAlgorithms["consistent-hashing"] {
		return nil
	}
	for i, t := range u.targets {
		if t.Failover != nil && *t.Failover {
			return c.fail(src.at(fmt.Sprintf("config.targets[%d].failover", i)), ConfigFieldInvalid,
				"%s %q target %d is a failover target, which consistent-hashing does not support", u.label, u.name, i)
		}
	}
	return nil
}

// defaultTargetsHealthchecks returns active and passive checks that mark a
// target unhealthy and healthy again.
func defaultTargetsHealthchecks() *kong.Healthchecks {
	threshold := func(t kong.HealthcheckThreshold) *kong.HealthcheckThreshold { return &t }
	return &kong.Healthchecks{
		Active: &kong.ActiveHealthcheck{
			Type: "tcp",
			Healthy: threshold(kong.HealthcheckThreshold{
				Interval:  floatPtr(targetsHealthcheckInterval),
				Successes: intPtr(targetsHealthcheckSuccesses),
			}),
			Unhealthy: threshold(kong.HealthcheckThreshold{
				Interval:    floatPtr(targetsHealthcheckInterval),
				TCPFailures: intPtr(targetsHealthcheckTCPFailures),
				Timeouts:    intPtr(targetsHealthcheckTimeouts),
			}),
		},
		Passive: &kong.PassiveHealthcheck{
			Type: "tcp",
			Unhealthy: threshold(kong.HealthcheckThreshold{
				TCPFailures: intPtr(targetsHealthcheckTCPFailures),
				Timeouts:    intPtr(targetsHealthcheckTimeouts),
			}),
		},
	}
}

func floatPtr(f float64) *float64 { return &f }
