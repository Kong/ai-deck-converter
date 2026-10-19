package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	valid := map[string]Version{
		"2.1":   {2, 1},
		"2.0":   {2, 0},
		"2.10":  {2, 10},
		"10.0":  {10, 0},
		"0.0":   {0, 0},
		"3.999": {3, 999},
	}
	for in, want := range valid {
		got, err := ParseVersion(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
		require.Equal(t, in, got.String(), in)
	}
	invalid := []string{
		"", "2", "2.", ".1", "2.1.0", "2.1.0-rc.4", "02.1", "2.01", "v2.1", "latest", " 2.1", "2.1 ",
	}
	for _, in := range invalid {
		_, err := ParseVersion(in)
		require.Error(t, err, "%q", in)
	}
}

func TestVersionLess(t *testing.T) {
	require.True(t, Version{2, 9}.Less(Version{2, 10}))
	require.False(t, Version{2, 10}.Less(Version{2, 9}))
	require.True(t, Version{1, 99}.Less(Version{2, 0}))
	require.False(t, Version{2, 1}.Less(Version{2, 1}))
}

// allFeatures lists every Feature constant. A new constant must be added here.
var allFeatures = []Feature{
	FeatureTargetCostList,
	FeaturePolicyCondition,
	FeatureMCPUpstreamProtocolVersion,
	FeatureACLWhen,
	FeaturePassthroughFormat,
	FeatureTypesafe,
	FeatureDecisionsCapability,
	FeatureSkillsCapability,
	FeatureFailoverHTTPCodes,
	FeatureBedrockVPCEndpoint,
	FeatureMCPTokenVault,
	FeatureProviderTokenVault,
	FeatureMCPRateLimiting,
}

func TestFeatureTable(t *testing.T) {
	require.Len(t, featureSince, len(allFeatures), "featureSince and allFeatures differ")
	for _, f := range allFeatures {
		since := Since(f)
		require.False(t, since.Less(SupportFloor), "%s: gate below the support floor", f)
		require.False(t, LatestVersion.Less(since), "%s: gate above the newest known release", f)
		require.True(t, Supports(f, since), f)
		require.True(t, Supports(f, LatestVersion), f)
		require.False(t, Supports(f, Version{since.Major, since.Minor - 1}), f)
	}
	require.Panics(t, func() { Since("unknown") })
}

func TestFeatureLookups(t *testing.T) {
	f, ok := FormatFeature("passthrough")
	require.True(t, ok)
	require.Equal(t, FeaturePassthroughFormat, f)
	f, ok = FormatFeature("typesafe")
	require.True(t, ok)
	require.Equal(t, FeatureTypesafe, f)
	_, ok = FormatFeature("openai")
	require.False(t, ok)
	_, ok = FormatFeature("vertex")
	require.False(t, ok)

	f, ok = CapabilityFeature("skills")
	require.True(t, ok)
	require.Equal(t, FeatureSkillsCapability, f)
	_, ok = CapabilityFeature("generate")
	require.False(t, ok)
}
