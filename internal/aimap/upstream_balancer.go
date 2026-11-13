package aimap

// UpstreamAlgorithms maps an agent balancer algorithm to the Kong Upstream
// algorithm that realizes it.
var UpstreamAlgorithms = map[string]string{
	"round-robin":        "round-robin",
	"consistent-hashing": "consistent-hashing",
	"least-connections":  "least-connections",
	"lowest-latency":     "latency",
}

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
