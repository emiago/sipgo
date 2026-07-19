package sip

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// TestTransportTCPClosesConnectionOnFramingError drives a real net.Pipe to show
// that a request the stream parser cannot frame (here, one with no Content-Length)
// causes the read loop to close the connection instead of reading on. If it read
// on, it would reuse a parser left dirty by the failed message and graft the
// peer's next bytes onto it — a stream desync. The loop returning is the close;
// pre-fix it looped only on ErrMessageTooLarge and this case hung waiting for more.
func TestTransportTCPClosesConnectionOnFramingError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	tp := &TransportTCP{}
	tp.init(NewParser())
	defer tp.Close()

	// Hold the same reference count initConnection gives a served connection, so
	// the deferred CloseAndDelete takes the branch that actually closes the socket
	// rather than the ref-went-negative one.
	conn := &TCPConnection{Conn: server, refcount: 1 + TransportIdleConnection}

	var delivered atomic.Int32
	done := make(chan struct{})
	go func() {
		tp.readConnection(conn, "127.0.0.1:5060", "127.0.0.2:5060", func(msg Message) {
			delivered.Add(1)
		})
		close(done)
	}()

	// A request with no Content-Length cannot be framed on a stream transport, so
	// the parser returns a hard error (ErrParseReadBodyIncomplete) rather than the
	// recoverable partial. The connection must be torn down on it.
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
