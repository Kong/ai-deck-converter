package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// upstreamOf returns the Upstream that the host of a URL-less service names.
// An Upstream without targets gives no targets. The service URL is used then.
func (r *Reverter) upstreamOf(svc *kong.Service) (*kong.Upstream, error) {
	up := r.findUpstream(svc)
	if up != nil && len(up.Targets) == 0 {
		return nil, r.warn("service %q: upstream %q has no targets; using the service url", svc.Name, up.Name)
	}
	return up, nil
}

// findUpstream returns the Upstream that the host of a URL-less service names.
func (r *Reverter) findUpstream(svc *kong.Service) *kong.Upstream {
	if svc.URL != "" {
		return nil
	}
	for i := range r.src.Upstreams {
		if r.src.Upstreams[i].Name == svc.Host {
			return &r.src.Upstreams[i]
		}
	}
	return nil
}

// liftedUpstream holds the config fields of an agent or an MCP server that
// revert gets from an Upstream and its Service.
type liftedUpstream struct {
	targets        []aigw.UpstreamTarget
	balancer       *aigw.UpstreamBalancer
	protocol, path string
}

// liftUpstream changes an Upstream and its Service to config fields.
// label names the entity in warnings.
func (r *Reverter) liftUpstream(label, name string, up *kong.Upstream, svc *kong.Service) (liftedUpstream, error) {
	var out liftedUpstream
	for _, t := range up.Targets {
		out.targets = append(out.targets, aigw.UpstreamTarget{
			Address: t.Target, Weight: t.Weight, Failover: t.Failover,
		})
	}
	// Kong defaults a Service protocol to http. The targets default is https.
	switch protocol := svc.Protocol; protocol {
	case "", "http":
		out.protocol = "http"
	case "https":
	default:
		if err := r.warn("%s %q: service %q has unsupported protocol %q; protocol dropped",
			label, name, svc.Name, protocol); err != nil {
			return out, err
		}
	}
	out.path = svc.Path

	algorithm, ok := aimap.UpstreamAlgorithmFromKong(up.Algorithm)
	if !ok {
		return out, r.warn("%s %q: upstream %q has unsupported algorithm %q; balancer dropped",
			label, name, up.Name, up.Algorithm)
	}
	switch algorithm {
	case "round-robin":
	case "consistent-hashing":
		if up.HashOn != "header" || up.HashOnHeader == "" {
			return out, r.warn("%s %q: upstream %q hashes on %q, not on a header; balancer dropped",
				label, name, up.Name, up.HashOn)
		}
		out.balancer = &aigw.UpstreamBalancer{Algorithm: algorithm, HashOnHeader: up.HashOnHeader}
	default:
		out.balancer = &aigw.UpstreamBalancer{Algorithm: algorithm}
	}
	return out, nil
}
