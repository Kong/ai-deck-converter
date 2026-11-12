package aimap

// UpstreamAlgorithms maps an agent balancer algorithm to the Kong Upstream
// algorithm that realizes it.
var UpstreamAlgorithms = map[string]string{
	"round-robin":        "round-robin",
	"consistent-hashing": "consistent-hashing",
	"least-connections":  "least-connections",
	"lowest-latency":     "latency",
}

// DefaultUpstreamHashHeader is the hash_on_header default of an agent balancer.
const DefaultUpstreamHashHeader = "X-Kong-LLM-Request-ID"

// UpstreamAlgorithmFromKong returns the agent balancer algorithm of a Kong
// Upstream algorithm. An empty value means Kong's round-robin default.
func UpstreamAlgorithmFromKong(kongAlgorithm string) (string, bool) {
	if kongAlgorithm == "" {
		return "round-robin", true
	}
	for aigw, k := range UpstreamAlgorithms {
		if k == kongAlgorithm {
			return aigw, true
		}
	}
	return "", false
}
