//go:build e2e

package e2e

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The harness speaks a minimal RFC 6455 subset: unfragmented text frames and
// close frames. This is enough to send realtime events through the gateway.
// It avoids a WebSocket library dependency for the converter module.

const (
	wsGUID        = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsOpText      = 0x1
	wsOpClose     = 0x8
	wsMaxPayload  = 1 << 20
	wsDialTimeout = 30 * time.Second
)

type wsConn struct {
	conn   net.Conn
	reader *bufio.Reader
	// masked is true on the client side. RFC 6455 requires clients to mask
	// every frame and servers to send no mask.
	masked bool
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec
	return base64.StdEncoding.EncodeToString(sum[:])
}

// serveWebSocketEcho completes the handshake and answers each text frame with
// a JSON event that wraps it. The gateway parses upstream realtime frames as
// JSON, so the reply must be JSON.
func serveWebSocketEcho(t *testing.T, w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		t.Logf("mock upstream: hijacking the WebSocket connection: %v", err)
		return
	}
	defer conn.Close()

	_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		wsAccept(r.Header.Get("Sec-WebSocket-Key")))
	if err := rw.Flush(); err != nil {
		return
	}

	ws := &wsConn{conn: conn, reader: rw.Reader}
	for {
		op, payload, err := ws.readFrame()
		if err != nil || op == wsOpClose {
			_ = ws.writeFrame(wsOpClose, nil)
			return
		}
		if op == wsOpText {
			reply := fmt.Sprintf(`{"type":"mock.echo","event":%s}`, payload)
			if err := ws.writeFrame(wsOpText, []byte(reply)); err != nil {
				return
			}
		}
	}
}

// dialWebSocket opens a WebSocket connection to rawURL. An https or wss URL
// uses TLS and accepts Kong's self-signed certificate. The result holds the
// handshake status, so a case can assert a rejected upgrade.
func dialWebSocket(t *testing.T, rawURL string, headers map[string]string) (*wsConn, int, string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", rawURL, err)
	}

	dialer := &net.Dialer{Timeout: wsDialTimeout}
	var conn net.Conn
	switch u.Scheme {
	case "wss", "https":
		conn, err = tls.DialWithDialer(dialer, "tcp", u.Host, &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
			ServerName:         u.Hostname(),
		})
	case "ws", "http":
		conn, err = dialer.Dial("tcp", u.Host)
	default:
		t.Fatalf("unsupported WebSocket URL scheme %q", u.Scheme)
	}
	if err != nil {
		t.Fatalf("dialing %s: %v", rawURL, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(wsDialTimeout))

	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("building handshake for %s: %v", rawURL, err)
	}
	req.URL.Scheme = map[string]string{"wss": "https", "ws": "http"}[u.Scheme]
	if req.URL.Scheme == "" {
		req.URL.Scheme = u.Scheme
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("writing handshake to %s: %v", rawURL, err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		t.Fatalf("reading handshake response from %s: %v", rawURL, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, wsMaxPayload))
		_ = resp.Body.Close()
		return nil, resp.StatusCode, string(body)
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != wsAccept(key) {
		t.Fatalf("handshake with %s returned Sec-WebSocket-Accept %q, want %q", rawURL, got, wsAccept(key))
	}
	return &wsConn{conn: conn, reader: reader, masked: true}, resp.StatusCode, ""
}

func (c *wsConn) sendText(t *testing.T, text string) {
	t.Helper()
	if err := c.writeFrame(wsOpText, []byte(text)); err != nil {
		t.Fatalf("sending WebSocket text frame: %v", err)
	}
}

func (c *wsConn) receiveText(t *testing.T) string {
	t.Helper()
	op, payload, err := c.readFrame()
	if err != nil {
		t.Fatalf("receiving WebSocket frame: %v", err)
	}
	if op != wsOpText {
		t.Fatalf("received WebSocket opcode %#x, want a text frame; payload: %q", op, payload)
	}
	return string(payload)
}

func (c *wsConn) close() {
	_ = c.writeFrame(wsOpClose, nil)
	_ = c.conn.Close()
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	header := []byte{0x80 | op}
	maskBit := byte(0)
	if c.masked {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		header = append(header, maskBit|byte(n))
	case n <= 0xFFFF:
		header = append(header, maskBit|126, 0, 0)
		binary.BigEndian.PutUint16(header[2:], uint16(n))
	default:
		header = append(header, maskBit|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[2:], uint64(n))
	}

	data := payload
	if c.masked {
		mask := make([]byte, 4)
		_, _ = rand.Read(mask)
		header = append(header, mask...)
		data = make([]byte, len(payload))
		for i := range payload {
			data[i] = payload[i] ^ mask[i%4]
		}
	}
	_, err := c.conn.Write(append(header, data...))
	return err
}

func (c *wsConn) readFrame() (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.reader, head[:]); err != nil {
		return 0, nil, err
	}
	if head[0]&0x80 == 0 {
		return 0, nil, errors.New("fragmented WebSocket frames are not supported")
	}
	op := head[0] & 0x0F

	n := uint64(head[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > wsMaxPayload {
		return 0, nil, fmt.Errorf("WebSocket frame of %d bytes exceeds the %d byte limit", n, wsMaxPayload)
	}

	var mask []byte
	if head[1]&0x80 != 0 {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(c.reader, mask); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, err
	}
	if mask != nil {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}
