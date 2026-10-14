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

func TestCheckMinRuntimeVersion(t *testing.T) {
	cases := []struct {
		dir, version string
		wantErr      string
	}{
		{"to-deck", "2.1", ""},
		{"to-dbless", "2.1", ""},
		{"from-deck", "", ""},
		{"to-deck", "", "-min-runtime-version is required"},
		{"to-dbless", "", "-min-runtime-version is required"},
		{"from-deck", "2.1", "not valid with from-deck"},
	}
	for _, tc := range cases {
		err := checkMinRuntimeVersion(tc.dir, tc.version)
		if tc.wantErr == "" {
			require.NoError(t, err, "%s %q", tc.dir, tc.version)
			continue
		}
		require.ErrorContains(t, err, tc.wantErr, "%s %q", tc.dir, tc.version)
	}
}
