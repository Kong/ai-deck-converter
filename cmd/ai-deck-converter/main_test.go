package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}
	_, err := detectDirection([]byte(":\tnot yaml"))
	require.Error(t, err, "invalid yaml: want error")
}
