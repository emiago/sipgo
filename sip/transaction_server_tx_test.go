package sip

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

func TestServerTransactionFSM(t *testing.T) {
	// SetTimers(1*time.Millisecond, 1*time.Millisecond, 1*time.Millisecond)
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "UDP", "127.0.0.2:5060")

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})

	t.Run("PassUpResponse", func(t *testing.T) {
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.2:5060": outgoing},
			},
		}
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		err = tx.Receive(req)
		require.NoError(t, err)

		require.NoError(t, tx.Err())
		select {
		case <-tx.Done():
			t.Error("Transaction should not terminate")
		default:
		}
	})

	t.Run("OutOfOrderResponse", func(t *testing.T) {
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.2:5060": outgoing},
			},
		}
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		// We received Cancel while dealing with resposn

		res100 := NewResponseFromRequest(req, StatusTrying, "Trying", nil)
		res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)

		require.NoError(t, tx.Respond(res200))
		require.NoError(t, tx.Respond(res100))
		require.NoError(t, tx.Respond(res100))

		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateAccepted))
	})

}

func TestServerTransactionNonInviteFSM(t *testing.T) {
	// SetTimers(1*time.Millisecond, 1*time.Millisecond, 1*time.Millisecond)

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})

	conn := &UDPConnection{
		PacketConn: &fakes.UDPConn{
			Reader:  incoming,
			Writers: map[string]io.Writer{"127.0.0.1:5060": outgoing},
		},
	}

	t.Run("UDP", func(t *testing.T) {
		req := testCreateRequest(t, "OPTIONS", "sip:example.com", "UDP", "127.0.0.1:5060")
		conn.Ref(1)
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)
		t.Cleanup(tx.Terminate)
		// Other FSM tests shorten the global timers. Keep this transaction
		// alive until cleanup so the Completed state can be inspected.
		tx.timer_j_time = time.Hour

		err = tx.Receive(req)
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateTrying))

		// passing 200 response
		err = tx.Respond(NewResponseFromRequest(req, 200, "OK", nil))
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateCompleted))

		// Timer j must be started
		tx.mu.Lock()
		timerJ := tx.timer_j
		tx.mu.Unlock()
		require.NotNil(t, timerJ)
	})

	t.Run("TCP", func(t *testing.T) {
		req := testCreateRequest(t, "OPTIONS", "sip:example.com", "TCP", "127.0.0.1:5060")
		conn.Ref(1)
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)
		t.Cleanup(tx.Terminate)

		err = tx.Receive(req)
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateTrying))

		// passing 200 response
		err = tx.Respond(NewResponseFromRequest(req, 200, "OK", nil))
		require.NoError(t, err)

		// timer J should be zero
		require.Zero(t, tx.timer_j_time)
		select {
		case <-tx.Done():
		case <-time.After(time.Second):
			t.Fatal("zero Timer J did not terminate the TCP transaction")
		}
		require.ErrorIs(t, tx.Err(), ErrTransactionTerminated)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateTerminated))
	})
}

func TestServerTransactionFSMInvite(t *testing.T) {
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})
	t.Run("InviteCancel", func(t *testing.T) {
		Timer_I = 10 * time.Millisecond
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.2:5060": outgoing},
			},
		}
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		// We received Cancel while dealing with resposn
		res100 := NewResponseFromRequest(req, StatusTrying, "Trying", nil)
		require.NoError(t, tx.Respond(res100))

		// Cancel will play
		cancelReq := NewRequest(CANCEL, req.Recipient)
		cancelReq.AppendHeader(HeaderClone(req.Via())) // Cancel request must match invite TOP via and only have that Via
		cancelReq.AppendHeader(HeaderClone(req.From()))
		cancelReq.AppendHeader(HeaderClone(req.To()))
		cancelReq.AppendHeader(HeaderClone(req.CallID()))

		require.NoError(t, tx.Receive(cancelReq))
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateCompleted))

		ack := NewRequest(ACK, req.Recipient)
		ack.AppendHeader(HeaderClone(req.Via())) // Cancel request must match invite TOP via and only have that Via
		ack.AppendHeader(HeaderClone(req.From()))
		ack.AppendHeader(HeaderClone(req.To()))
		ack.AppendHeader(HeaderClone(req.CallID()))
		require.NoError(t, tx.Receive(ack))

		require.Eventually(t, func() bool {
			return compareFunctions(tx.currentFsmState(), tx.inviteStateTerminated) == nil
		}, 10*Timer_I, Timer_I)
	})
}

func TestServerTransactionAckSendMissingCallID(t *testing.T) {
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")
	tx := NewServerTx("123", req, nil, slog.Default())
	ack := NewRequest(ACK, req.Recipient)

	close(tx.done)

	require.NotPanics(t, func() {
		tx.ackSend(ack)
	})
}

func TestServerTransactionContext(t *testing.T) {
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")
	tx := NewServerTx("123", req, nil, slog.Default())
	ctx := ServerTransactionContext(tx)
	tx.Terminate()
	require.Equal(t, context.Canceled, ctx.Err())
	require.Equal(t, ErrTransactionTerminated, tx.Err())
}

func TestServerTransactionReleasesConnRef(t *testing.T) {
	req := testCreateRequest(t, "OPTIONS", "sip:example.com", "UDP", "127.0.0.1:5060")
	conn := &UDPConnection{
		PacketConn: &fakes.UDPConn{
			Reader:  bytes.NewBuffer([]byte{}),
			Writers: map[string]io.Writer{},
		},
	}
	conn.Ref(2) // serverRequestConnection holds a reference before NewServerTx

	tx := NewServerTx("123", req, conn, slog.Default())
	require.NoError(t, tx.Init())

	tx.Terminate()
	<-tx.Done()

	require.Equal(t, 1, conn.Ref(0), "Terminate must release exactly one connection reference")
}

func newRejectedInviteTx(t *testing.T, transport string, status int) (*ServerTx, *Request) {
	t.Helper()
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", transport, "127.0.0.2:5060")
	conn := &UDPConnection{PacketConn: &fakes.UDPConn{
		Reader:  bytes.NewBuffer(nil),
		Writers: map[string]io.Writer{"127.0.0.2:5060": io.Discard},
	}}
	conn.Ref(1)
	key, err := ServerTxKeyMake(req)
	require.NoError(t, err)
	tx := NewServerTx(key, req, conn, slog.Default())
	require.NoError(t, tx.Init())
	t.Cleanup(tx.Terminate)
	require.NoError(t, tx.Respond(NewResponseFromRequest(req, status, "Rejected", nil)))
	return tx, req
}

func TestReliableRejectedInviteWaitsForAck(t *testing.T) {
	for _, transport := range []string{"TCP", "TLS"} {
		for _, status := range []int{300, 486, 491, 603} {
			t.Run(fmt.Sprintf("%s/%d", transport, status), func(t *testing.T) {
				tx, req := newRejectedInviteTx(t, transport, status)
				tx.TerminateGracefully()
				select {
				case <-tx.Done():
					t.Fatal("rejected INVITE transaction terminated before ACK")
				default:
				}

				ack := req.Clone()
				ack.Method = ACK
				ack.CSeq().MethodName = ACK
				key, err := ServerTxKeyMake(ack)
				require.NoError(t, err)
				require.Equal(t, tx.Key(), key)

				layer := &TransactionLayer{serverTransactions: newTransactionStore[*ServerTx]()}
				layer.serverTransactions.items[key] = tx
				layer.reqHandler = func(*Request, *ServerTx) { t.Error("non-2xx ACK reached request handler") }
				tx.OnTerminate(layer.serverTxTerminate)
				require.NoError(t, layer.serverTxRequest(ack, key))
				select {
				case <-tx.Done():
				case <-time.After(time.Second):
					t.Fatal("ACK did not terminate reliable INVITE transaction")
				}
			})
		}
	}
}

func TestReliableRejectedInviteTimesOutWithoutAck(t *testing.T) {
	tx, _ := newRejectedInviteTx(t, "TLS", 491)
	tx.TerminateGracefully()
	select {
	case <-tx.Done():
		t.Fatal("rejected INVITE transaction terminated before timer H")
	default:
	}
	tx.mu.Lock()
	timer := tx.timer_h
	tx.mu.Unlock()
	require.NotNil(t, timer)
	tx.mu.Lock()
	timer.Reset(10 * time.Millisecond)
	tx.mu.Unlock()
	select {
	case <-tx.Done():
	case <-time.After(time.Second):
		t.Fatal("timer H did not clean up unacknowledged INVITE")
	}
}

func TestReliableTransactionCleanupWithoutPendingAck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method RequestMethod
		status int
	}{
		{"unanswered INVITE", INVITE, 0},
		{"provisional INVITE", INVITE, 180},
		{"accepted INVITE", INVITE, 200},
		{"rejected OPTIONS", OPTIONS, 491},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := testCreateRequest(t, string(tc.method), "sip:127.0.0.99:5060", "TLS", "127.0.0.2:5060")
			conn := &UDPConnection{PacketConn: &fakes.UDPConn{Reader: bytes.NewBuffer(nil), Writers: map[string]io.Writer{"127.0.0.2:5060": io.Discard}}}
			conn.Ref(1)
			tx := NewServerTx("cleanup", req, conn, slog.Default())
			require.NoError(t, tx.Init())
			t.Cleanup(tx.Terminate)
			if tc.status != 0 {
				require.NoError(t, tx.Respond(NewResponseFromRequest(req, tc.status, "Response", nil)))
			}
			tx.TerminateGracefully()
			select {
			case <-tx.Done():
			default:
				t.Fatal("transaction without a pending non-2xx INVITE ACK was retained")
			}
		})
	}
}
