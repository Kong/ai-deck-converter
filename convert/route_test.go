package convert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportProtocols(t *testing.T) {
	for _, tc := range []struct {
		in        []string
		websocket bool
		want      []string
	}{
		{nil, false, nil},
		{[]string{"https"}, false, []string{"https"}},
		{[]string{"ws", "wss"}, false, []string{"http", "https"}},
		{[]string{"http", "ws"}, false, []string{"http"}},
		{nil, true, []string{"ws", "wss"}},
		{[]string{"https"}, true, []string{"wss"}},
		{[]string{"grpc"}, true, []string{"ws", "wss"}},
		{[]string{"http", "ws", "wss"}, true, []string{"ws", "wss"}},
	} {
		require.Equal(t, tc.want, transportProtocols(tc.in, tc.websocket), "%v websocket=%v", tc.in, tc.websocket)
	}
}
