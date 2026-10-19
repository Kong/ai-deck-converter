package aimap

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is an AI Gateway minor release, for example 2.1.
type Version struct {
	Major int
	Minor int
}

// SupportFloor is the oldest AI Gateway release that the converter supports.
var SupportFloor = mustVersion("2.0")

// LatestVersion is the newest AI Gateway release that the converter knows.
var LatestVersion = mustVersion("2.3")

// versionPattern is the pattern of the Konnect min_runtime_version field. It
// also rejects leading zeros, so each version has one spelling.
var versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// ParseVersion parses a MAJOR.MINOR version.
func ParseVersion(s string) (Version, error) {
	parts := versionPattern.FindStringSubmatch(s)
	if parts == nil {
		return Version{}, fmt.Errorf("version %q is not in the form MAJOR.MINOR, for example %q", s, "2.1")
	}
	major, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: %w", s, err)
	}
	minor, err := strconv.Atoi(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: %w", s, err)
	}
	return Version{Major: major, Minor: minor}, nil
}

// mustVersion parses s as a MAJOR.MINOR version. It panics on an invalid s,
// so callers must only use it on a literal known at compile time.
func mustVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Less reports whether v is an older release than o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	return v.Minor < o.Minor
}

// Feature names an output of the converter that some AI Gateway releases do
// not accept.
type Feature string

// Features with a minimum AI Gateway release.
const (
	FeatureTargetCostList             Feature = "target-cost-list"
	FeaturePolicyCondition            Feature = "policy-condition"
	FeatureMCPUpstreamProtocolVersion Feature = "mcp-upstream-protocol-version"
	FeatureACLWhen                    Feature = "acl-when"
	FeaturePassthroughFormat          Feature = "passthrough-format"
	FeatureTypesafe                   Feature = "typesafe"
	FeatureDecisionsCapability        Feature = "decisions-capability"
	FeatureSkillsCapability           Feature = "skills-capability"
	FeatureFailoverHTTPCodes          Feature = "failover-http-codes"
	FeatureBedrockVPCEndpoint         Feature = "bedrock-vpc-endpoint"
	FeatureMCPTokenVault              Feature = "mcp-token-vault" //nolint:gosec // a feature name, not a credential
	FeatureMCPRateLimiting            Feature = "mcp-rate-limiting"
)

// featureSince maps each feature to its first AI Gateway release. The source
// is the x-min-runtime-version annotations of the Konnect AI Gateway API spec.
var featureSince = map[Feature]Version{
	FeatureTargetCostList:             mustVersion("2.1"),
	FeaturePolicyCondition:            mustVersion("2.1"),
	FeatureMCPUpstreamProtocolVersion: mustVersion("2.1"),
	FeatureACLWhen:                    mustVersion("2.2"),
	FeaturePassthroughFormat:          mustVersion("2.2"),
	FeatureTypesafe:                   mustVersion("2.2"),
	FeatureDecisionsCapability:        mustVersion("2.2"),
	FeatureSkillsCapability:           mustVersion("2.2"),
	FeatureFailoverHTTPCodes:          mustVersion("2.3"),
	FeatureBedrockVPCEndpoint:         mustVersion("2.3"),
	FeatureMCPTokenVault:              mustVersion("2.3"),
	FeatureMCPRateLimiting:            mustVersion("2.3"),
}

// Since returns the first AI Gateway release that has f. It panics for an
// unknown feature, because that is a converter bug.
func Since(f Feature) Version {
	v, ok := featureSince[f]
	if !ok {
		panic(fmt.Sprintf("aimap: feature %q has no minimum version", f))
	}
	return v
}

// Supports reports whether release v has feature f.
func Supports(f Feature, v Version) bool {
	return !v.Less(Since(f))
}

// TargetCostListKeys are the target-config keys that FeatureTargetCostList
// gates.
var TargetCostListKeys = []string{"input_cost_list", "output_cost_list", "cache_read_cost_list"}

// FailoverHTTPCodes are the failover_criteria values that
// FeatureFailoverHTTPCodes gates.
var FailoverHTTPCodes = map[string]bool{
	"http_400": true, "http_401": true, "http_402": true, "http_405": true,
	"http_408": true, "http_409": true, "http_413": true, "http_415": true,
	"http_422": true, "http_522": true, "http_529": true, "http_5xx": true,
}

// TypesafeProviderType is the provider type that FeatureTypesafe gates.
const TypesafeProviderType = "typesafe"

var formatFeatures = map[string]Feature{
	PassthroughFormat:    FeaturePassthroughFormat,
	TypesafeProviderType: FeatureTypesafe,
}

// FormatFeature returns the feature that gates the model format.
func FormatFeature(format string) (Feature, bool) {
	f, ok := formatFeatures[NormalizeFormat(format)]
	return f, ok
}

var capabilityFeatures = map[string]Feature{
	"decisions": FeatureDecisionsCapability,
	"skills":    FeatureSkillsCapability,
}

// CapabilityFeature returns the feature that gates the canonical capability.
func CapabilityFeature(capability string) (Feature, bool) {
	f, ok := capabilityFeatures[capability]
	return f, ok
}
