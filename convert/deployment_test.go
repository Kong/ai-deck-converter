package convert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIssuesSafeToDeploy(t *testing.T) {
	require.True(t, IssuesSafeToDeploy(nil))
	require.True(t, IssuesSafeToDeploy([]ConversionIssue{}))
	require.True(t, IssuesSafeToDeploy([]ConversionIssue{
		{Severity: Warning, Code: BehaviorMayDiffer, Reason: "x"},
	}))
	require.False(t, IssuesSafeToDeploy([]ConversionIssue{
		{Severity: Error, Code: UndefinedReference, Reason: "x"},
	}))
	require.False(t, IssuesSafeToDeploy([]ConversionIssue{
		{Severity: Warning, Code: RoutingConflict, Reason: "overlap"},
	}))
}

func TestConversionResultSafeToDeployMetadata(t *testing.T) {
	dir := filepath.Join("testdata", "92_routing_conflicts")
	src, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
	require.NoError(t, err)
	result, err := Convert(src, testOptions)
	require.NoError(t, err)
	require.False(t, result.SafeToDeploy())
	require.False(t, result.Metadata.SafeToDeploy)
	require.True(t, result.HasErrors() == false)
}
