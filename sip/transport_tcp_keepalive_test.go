package sip

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReadConnectionKeepAliveMidMessage feeds the read loop a message split so
// that the LF of a header line's CRLF arrives as a read of its own. That read
// used to be taken for an RFC 5626 keep alive and dropped, leaving the parser on
// a bare CR, so the next message failed to frame and so did every one after it.
//
// The split point is TCP's choice, so every CRLF in the message is tried.
func TestReadConnectionKeepAliveMidMessage(t *testing.T) {
	first := testRawOptions("mid-message")
	second := testRawOptions("after-the-split")

	for i := 0; i+1 < len(first); i++ {
		if first[i] != '\r' || first[i+1] != '\n' {
			continue
		}
		rest := append(append([]byte{}, first[i+2:]...), second...)
		callIDs := readConnectionOverPipe(t, 2, first[:i+1], first[i+1:i+2], rest)
		require.Equal(t, []string{"mid-message", "after-the-split"}, callIDs,
			"a read boundary inside the CRLF at byte %d lost a message", i)
	}
}

// readConnectionOverPipe drives readConnection over a net.Pipe, which is
// unbuffered, so one Write is one Read and the test picks the read boundaries.
// It returns the Call-ID of every message handed to the handler, giving up once
// it has want of them or the connection has gone quiet.
func readConnectionOverPipe(t *testing.T, want int, writes ...[]byte) []string {
	t.Helper()

	tcp := &TransportTCP{}
	tcp.init(NewParser())

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	got := make(chan Message, 8)
	go tcp.readConnection(&TCPConnection{Conn: serverConn, refcount: 1},
		serverConn.LocalAddr().String(), serverConn.RemoteAddr().String(),
		func(msg Message) { got <- msg })

	// Discard anything written back, so a pong cannot block the read loop.
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := clientConn.Read(buf); err != nil {
				return
			}
		}
	}()

	for _, w := range writes {
		require.NoError(t, clientConn.SetWriteDeadline(time.Now().Add(2*time.Second)))
		_, err := clientConn.Write(w)
		require.NoError(t, err)
	}

	var callIDs []string
	for len(callIDs) < want {
		select {
		case msg := <-got:
			callID := msg.CallID()
			require.NotNil(t, callID)
			callIDs = append(callIDs, callID.Value())
		case <-time.After(2 * time.Second):
			return callIDs
		}
	}
	return callIDs
}
