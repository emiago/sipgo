package sip

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

// Timer J is zero on reliable transports. Concurrent retransmissions keep
// fsmMu contended so the timer can acquire it before a separate Err() call.
// Respond must return the result of sending, even if Timer J then terminates.
func TestServerTxRespondNonInviteFinalOverReliableTransport(t *testing.T) {
	cases := []struct {
		transport string
		method    string
		status    int
	}{
		{"TCP", "UPDATE", StatusOK},
		{"TCP", "PRACK", StatusBadRequest},
		{"TLS", "UPDATE", StatusOK},
		{"WS", "PRACK", StatusBadRequest},
		{"WSS", "UPDATE", StatusOK},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s/%d", tc.transport, tc.method, tc.status), func(t *testing.T) {
			const rounds = 100
			for round := 0; round < rounds; round++ {
				req := testCreateNonInvite(t, tc.method, tc.transport, "tag")
				outgoing := bytes.NewBuffer(nil)
				// A fake stream connection isolates the FSM's reliable-transport behavior.
				conn := &TCPConnection{refcount: 2, Conn: &fakes.TCPConn{
					Reader: bytes.NewBuffer(nil),
					Writer: outgoing,
				}}
				tx := NewServerTx("key", req, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))
				require.NoError(t, tx.Init())
				require.True(t, tx.reliable)
				require.Zero(t, tx.timer_j_time)

				// Exercise both Trying and Proceeding before the final response.
				if round%2 == 0 {
					require.NoError(t, tx.Respond(NewResponseFromRequest(req, StatusTrying, "Trying", nil)))
					outgoing.Reset()
				}
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

				res := NewResponseFromRequest(req, tc.status, "Final", nil)
				err := tx.Respond(res)
				close(stop)
				wg.Wait()

				require.NoError(t, err, "round %d: a sent response was reported as failed", round)
				require.Contains(t, outgoing.String(), res.StartLine(), "round %d: final response was not written", round)
				select {
				case <-tx.Done():
				case <-time.After(time.Second):
					t.Fatal("Timer J did not terminate the transaction")
				}
				// Err acquires fsmMu and waits for timer teardown to finish.
				require.ErrorIs(t, tx.Err(), ErrTransactionTerminated)
				require.Equal(t, 1, conn.Ref(0), "Timer J must release the transaction reference")
				written := outgoing.Len()
				require.ErrorIs(t, tx.Respond(res), ErrTransactionTerminated)
				require.Equal(t, written, outgoing.Len(), "a terminated transaction must not send another response")
			}
		})
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

// A write failure must remain observable even though the FSM also terminates.
func TestServerTxRespondPreservesTransportError(t *testing.T) {
	for _, status := range []int{StatusOK, StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			req := testCreateNonInvite(t, "UPDATE", "TCP", "tag")
			conn := &TCPConnection{refcount: 2, Conn: &fakes.TCPConn{
				Reader: bytes.NewBuffer(nil),
				Writer: finalResponseErrorWriter{},
			}}
			tx := NewServerTx("key", req, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.NoError(t, tx.Init())
			err := tx.Respond(NewResponseFromRequest(req, status, fmt.Sprint(status), nil))
			require.ErrorIs(t, err, ErrTransactionTransport)
			require.ErrorIs(t, err, io.ErrClosedPipe)
			require.ErrorIs(t, tx.Err(), io.ErrClosedPipe)
			select {
			case <-tx.Done():
			default:
				t.Fatal("write failure did not terminate the transaction")
			}
			require.Equal(t, 1, conn.Ref(0))
		})
	}
}

type finalResponseErrorWriter struct{}

func (finalResponseErrorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// UDP must retain the final response for retransmission until Timer J fires.
func TestServerTxRespondNonInviteFinalOverUDP(t *testing.T) {
	for _, status := range []int{StatusOK, StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			req := testCreateNonInvite(t, "UPDATE", "UDP", "tag")
			outgoing := bytes.NewBuffer(nil)
			conn := &UDPConnection{PacketConn: &fakes.UDPConn{
				Reader:  bytes.NewBuffer(nil),
				Writers: map[string]io.Writer{req.Source(): outgoing},
			}}
			conn.Ref(2)
			tx := NewServerTx("key", req, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.NoError(t, tx.Init())
			defer tx.Terminate()
			require.False(t, tx.reliable)
			require.Equal(t, Timer_J, tx.timer_j_time)
			// Keep the real timer from expiring before the explicit timer event below.
			tx.timer_j_time = time.Hour

			res := NewResponseFromRequest(req, status, "Final", nil)
			require.NoError(t, tx.Respond(res))
			require.Contains(t, outgoing.String(), res.StartLine())
			require.NoError(t, tx.Err())
			select {
			case <-tx.Done():
				t.Fatal("UDP transaction terminated before Timer J")
			default:
			}
			require.Equal(t, 2, conn.Ref(0))
			first := outgoing.String()
			require.NoError(t, tx.Receive(req))
			require.Equal(t, first+first, outgoing.String(), "retransmission must resend the final response")

			tx.spinFsm(server_input_timer_j)
			require.ErrorIs(t, tx.Err(), ErrTransactionTerminated)
			select {
			case <-tx.Done():
			default:
				t.Fatal("Timer J did not terminate the UDP transaction")
			}
			require.Equal(t, 1, conn.Ref(0))
		})
	}
}
