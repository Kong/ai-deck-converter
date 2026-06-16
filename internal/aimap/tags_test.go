package aimap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelsTagsRoundTrip(t *testing.T) {
	labels := map[string]string{"env": "prod", "team": "ai-platform"}

	for _, prefix := range []string{"", "aigw/"} {
		tags := LabelsToTags(labels, prefix)
		got, rest := TagsToLabels(tags, prefix)
		require.Equal(t, labels, got, "prefix %q: round trip", prefix)
		require.Empty(t, rest, "prefix %q: unexpected unconverted tags", prefix)
	}
}

func TestTagsToLabelsSkipsForeignTags(t *testing.T) {
	labels, rest := TagsToLabels([]string{"aigw/env:prod", "no-separator", "other/x:y"}, "aigw/")
	require.Equal(t, map[string]string{"env": "prod"}, labels)
	require.Equal(t, []string{"no-separator", "other/x:y"}, rest)
}
