package sip

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTransportLayerKeepaliveHandlerTCP verifies the keepalive handler fires at
// the transport's own CRLF-keepalive detection point, distinguishing a 2-byte
// pong (isPing=false) from a 4-byte ping (isPing=true), and that a keepalive is
// never forwarded to the SIP message handler.
func TestTransportLayerKeepaliveHandlerTCP(t *testing.T) {
	type kev struct {
		info   TransportReadProps
		isPing bool
	}
	calls := make(chan kev, 2)
	tcp := &TransportTCP{
		keepaliveHandler: func(info TransportReadProps, isPing bool) {
			calls <- kev{info, isPing}
		},
	}
	tcp.init(NewParser())

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	conn := &TCPConnection{
		Conn:     serverConn,
		refcount: 1,
	}
	go tcp.readConnection(conn, serverConn.LocalAddr().String(), serverConn.RemoteAddr().String(), func(msg Message) {
		t.Errorf("SIP handler must not be called for a CRLF keepalive, got %v", msg)
	})

	// Single CRLF = pong (the peer's reply to our ping) -> isPing=false.
	_, err := clientConn.Write([]byte("\r\n"))
	require.NoError(t, err)
	select {
	case ev := <-calls:
		require.False(t, ev.isPing, "single CRLF must be reported as a pong")
		require.Equal(t, "TCP", ev.info.Transport)
		require.NotNil(t, ev.info.LocalAddr)
		require.NotNil(t, ev.info.RemoteAddr)
	case <-time.After(2 * time.Second):
		t.Fatal("expected keepalive handler call for pong")
	}

	// Double CRLF = ping (the transport also auto-pongs it) -> isPing=true.
	_, err = clientConn.Write([]byte("\r\n\r\n"))
	require.NoError(t, err)
	select {
	case ev := <-calls:
		require.True(t, ev.isPing, "double CRLF must be reported as a ping")
	case <-time.After(2 * time.Second):
		t.Fatal("expected keepalive handler call for ping")
	}

	// Drain the auto-pong so the unbuffered net.Pipe write in the read goroutine
	// doesn't block (it writes "\r\n" back for the ping).
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = io.ReadFull(clientConn, make([]byte, 2))
}
