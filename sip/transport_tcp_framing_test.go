package sip

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// A request the parser cannot frame must end the read loop, not leave a dirty parser
// for the peer's next bytes to graft onto.
func TestTransportTCPClosesConnectionOnFramingError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	tp := &TransportTCP{}
	tp.init(NewParser())
	defer tp.Close()

	// Same refcount initConnection gives a served connection, so the deferred
	// CloseAndDelete actually closes the socket.
	conn := &TCPConnection{Conn: server, refcount: 1 + TransportIdleConnection}

	var delivered atomic.Int32
	done := make(chan struct{})
	go func() {
		tp.readConnection(conn, "127.0.0.1:5060", "127.0.0.2:5060", func(msg Message) {
			delivered.Add(1)
		})
		close(done)
	}()

	// No Content-Length on a stream transport, so the parser returns
	// ErrParseReadBodyIncomplete rather than the recoverable partial.
	msg := "INVITE sip:victim@evil SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP evil;branch=z9hG4bKEVIL\r\n" +
		"From: <sip:attacker@evil>;tag=1\r\n" +
		"To: <sip:victim@evil>\r\n" +
		"Call-ID: framing-desync@evil\r\n" +
		"CSeq: 1 INVITE\r\n\r\n"
	if _, err := client.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("read loop did not return: a framing error left the connection open")
	}
	if n := delivered.Load(); n != 0 {
		t.Fatalf("no message may be delivered from an unframed stream, got %d", n)
	}
}
