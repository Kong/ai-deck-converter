package revert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var handwrittenIntelligent = filepath.Join("testdata", "76_intelligent_balancer_handwritten", "input.yaml")

func TestIntelligentBalancerWarnings(t *testing.T) {
	in, err := os.ReadFile(handwrittenIntelligent)
	require.NoError(t, err)
	_, warnings, err := Revert(in, Options{})
	require.NoError(t, err)
	require.Equal(t, []string{
		`model "near": cost_quality_tradeoff 0.4 is not a preset value. ` +
			`It becomes the nearest preset "balanced", with value 0.5.`,
		`model "margin": margin 12 is not a preset value. It becomes the nearest preset "cost", with margin 15.`,
		`model "margin": cost_quality_tradeoff 0.5 becomes 0.25, the value of preset "cost".`,
		`model "orphan": no model with the decisions capability has the classifier target "kev". ` +
			`classifier.model is left out. Set it to a model with the decisions capability and one target.`,
	}, warnings)
}

func TestIntelligentBalancerStrict(t *testing.T) {
	in, err := os.ReadFile(handwrittenIntelligent)
	require.NoError(t, err)
	_, _, err = Revert(in, Options{Strict: true})
	require.ErrorContains(t, err, "cost_quality_tradeoff 0.4 is not a preset value")
}
