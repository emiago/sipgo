package sip

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

// Responses received back to back must reach the TU in the order they were
// received.
//
// The transport layer delivers every message in its own goroutine. Without
// ordering, the goroutines carrying 180 and 200 of the same transaction race
// for fsmMu; when the 200 wins, the transaction moves to "Accepted" and the
// 180 is then discarded by that state as a stray response (RFC 6026 7.2).
// The TU never sees the 180, which in a call means losing ringing and any
// early media announcement carried by it.
//
// Repeated, because the loss depends on goroutine scheduling: on the code
// before this fix the 180 was lost in nearly every iteration.
func TestTransactionLayerResponseOrder(t *testing.T) {
	const iterations = 20

	for i := 0; i < iterations; i++ {
		txl, tx, req := testResponseOrderSetup(t)

		ringing := NewResponseFromRequest(req, StatusRinging, "Ringing", nil)
		ok := NewResponseFromRequest(req, StatusOK, "OK", nil)

		// Same as the transport layer does: both responses handed over by
		// the receiving goroutine, one after another.
		txl.handleMessage(ringing)
		txl.handleMessage(ok)

		first := testReceiveResponse(t, tx)
		require.Equal(t, StatusRinging, first.StatusCode, "provisional response lost or reordered")

		second := testReceiveResponse(t, tx)
		require.Equal(t, StatusOK, second.StatusCode)

		tx.Terminate()
	}
}

func testResponseOrderSetup(t *testing.T) (*TransactionLayer, *ClientTx, *Request) {
	t.Helper()

	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")
	req.raddr = Addr{IP: net.ParseIP("127.0.0.99"), Port: 5060}

	conn := &UDPConnection{
		PacketConn: &fakes.UDPConn{
			Reader:  bytes.NewBuffer([]byte{}),
			Writers: map[string]io.Writer{"127.0.0.99:5060": bytes.NewBuffer([]byte{})},
		},
	}

	key, err := ClientTxKeyMake(req)
	require.NoError(t, err)

	tx := NewClientTx(key, req, conn, slog.Default())
	require.NoError(t, tx.Init())

	txl := &TransactionLayer{
		clientTransactions: newTransactionStore[*ClientTx](),
		serverTransactions: newTransactionStore[*ServerTx](),
		reqHandler:         defaultRequestHandler,
		unRespHandler:      defaultUnhandledRespHandler,
		log:                DefaultLogger(),
	}
	txl.clientTransactions.put(key, tx)
	t.Cleanup(func() { tx.Terminate() })

	return txl, tx, req
}

func testReceiveResponse(t *testing.T, tx *ClientTx) *Response {
	t.Helper()

	select {
	case res := <-tx.Responses():
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("no response passed up to TU")
	}
	return nil
}
