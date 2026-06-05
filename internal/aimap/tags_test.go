package aimap

import (
	"reflect"
	"testing"
)

func TestLabelsTagsRoundTrip(t *testing.T) {
	labels := map[string]string{"env": "prod", "team": "ai-platform"}

	for _, prefix := range []string{"", "aigw/"} {
		tags := LabelsToTags(labels, prefix)
		got, rest := TagsToLabels(tags, prefix)
		if !reflect.DeepEqual(got, labels) {
			t.Errorf("prefix %q: round trip = %v, want %v", prefix, got, labels)
		}
		if len(rest) != 0 {
			t.Errorf("prefix %q: unexpected unconverted tags %v", prefix, rest)
		}
	}
}

func TestTagsToLabelsSkipsForeignTags(t *testing.T) {
	labels, rest := TagsToLabels([]string{"aigw/env:prod", "no-separator", "other/x:y"}, "aigw/")
	if !reflect.DeepEqual(labels, map[string]string{"env": "prod"}) {
		t.Errorf("labels = %v", labels)
	}
	if !reflect.DeepEqual(rest, []string{"no-separator", "other/x:y"}) {
		t.Errorf("rest = %v", rest)
	}
}
