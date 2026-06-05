package main

import "testing"

func TestDetectDirection(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"deck", "_format_version: \"3.0\"\nservices: []\n", "from-deck"},
		{"aigw", "models:\n  - name: m1\n", "to-deck"},
		{"empty", "", "to-deck"},
	}
	for _, tc := range cases {
		got, err := detectDirection([]byte(tc.in))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: detectDirection = %q, want %q", tc.name, got, tc.want)
		}
	}
	if _, err := detectDirection([]byte(":\tnot yaml")); err == nil {
		t.Error("invalid yaml: want error")
	}
}
