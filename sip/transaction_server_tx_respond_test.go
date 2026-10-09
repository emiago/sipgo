package sip

import (
	"bytes"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

// Respond must not report a final response it has sent as failed.
//
// Timer J is zero on reliable transports (RFC 3261 section 17.2.2), so the
// time.AfterFunc armed by actFinal is runnable as soon as the FSM spin
// releases fsmMu. Reading the error after that release let the timer
// goroutine terminate the transaction in between, and Respond returned
// ErrTransactionTerminated for a response already written to the connection.
//
// Non-INVITE, because actFinal and Timer J belong to that state machine.
//
// The retransmissions are what make this reproducible rather than lucky: they
// keep fsmMu contended, so it is in starvation mode and hands off to the
// waiting timer goroutine instead of letting Respond reacquire it.
func TestServerTxRespondNonInviteFinalOverReliableTransport(t *testing.T) {
	const rounds = 150

	for round := 0; round < rounds; round++ {
		req := testCreateNonInvite(t, "UPDATE", "TCP", "tag")
		outgoing := bytes.NewBuffer([]byte{})
		conn := &TCPConnection{Conn: &fakes.TCPConn{
			Reader: bytes.NewBuffer(nil),
			Writer: outgoing,
		}}

		tx := NewServerTx("key", req, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, tx.Init())

		stop := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						tx.Receive(req)
					}
				}
			}()
		}
		time.Sleep(2 * time.Millisecond)

		err := tx.Respond(NewResponseFromRequest(req, StatusOK, "OK", nil))
		close(stop)
		wg.Wait()

		require.NoError(t, err, "round %d: a response that was written to the connection was reported as failed", round)
		require.NotEmpty(t, outgoing.Bytes(), "round %d: nothing was written", round)
	}
}

func testCreateNonInvite(t testing.TB, method, transport, tag string) *Request {
	t.Helper()
	return testCreateMessage(t, []string{
		method + " sip:127.0.0.99:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " 127.0.0.2:5060;branch=" + GenerateBranch(),
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=" + tag,
		"To: \"Bob\" <sip:127.0.0.99:5060>",
		"Call-ID: gotest-" + tag,
		"CSeq: 1 " + method,
		"Content-Length: 0",
		"",
		"",
	}).(*Request)
}
