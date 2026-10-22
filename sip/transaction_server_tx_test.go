package sip

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
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
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		err = tx.Receive(req)
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateTrying))

		// passing 200 response
		err = tx.Respond(NewResponseFromRequest(req, 200, "OK", nil))
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateCompleted))

		// Timer j must be started
		require.NotNil(t, tx.timer_j)
	})

	t.Run("TCP", func(t *testing.T) {
		req := testCreateRequest(t, "OPTIONS", "sip:example.com", "TCP", "127.0.0.1:5060")
		tx := NewServerTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		err = tx.Receive(req)
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateTrying))

		// passing 200 response
		err = tx.Respond(NewResponseFromRequest(req, 200, "OK", nil))
		require.NoError(t, err)
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.stateCompleted))

		// timer J should be zero
		require.Zero(t, tx.timer_j_time)
		require.Zero(t, <-tx.done)
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

type serverResponseConn struct {
	writeErr error
	writes   atomic.Int64
}

func (c *serverResponseConn) LocalAddr() net.Addr    { return &net.TCPAddr{} }
func (c *serverResponseConn) Ref(int) int            { return 1 }
func (c *serverResponseConn) TryClose() (int, error) { return 1, nil }
func (c *serverResponseConn) Close() error           { return nil }
func (c *serverResponseConn) WriteMsg(Message) error {
	c.writes.Add(1)
	return c.writeErr
}

func newServerResponseTx(t testing.TB, method, transport string, conn Connection) *ServerTx {
	t.Helper()
	req := testCreateRequest(t, method, "sip:example.com", transport, "127.0.0.1:5061")
	tx := NewServerTx("response-test", req, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, tx.Init())
	return tx
}

func TestServerTransactionRespondResult(t *testing.T) {
	writeErr := errors.New("response write failed")
	for _, method := range []string{"BYE", "INVITE"} {
		t.Run(method, func(t *testing.T) {
			for _, transport := range []string{"UDP", "TCP", "TLS", "WS", "WSS"} {
				t.Run(transport, func(t *testing.T) {
					for _, status := range []int{StatusTrying, StatusOK, StatusBadRequest} {
						t.Run(strconv.Itoa(status), func(t *testing.T) {
							conn := &serverResponseConn{writeErr: writeErr}
							tx := newServerResponseTx(t, method, transport, conn)
							defer tx.Terminate()
							err := tx.Respond(NewResponseFromRequest(tx.Origin(), status, strconv.Itoa(status), nil))
							require.ErrorIs(t, err, ErrTransactionTransport)
							require.ErrorContains(t, err, writeErr.Error())
							require.EqualValues(t, 1, conn.writes.Load())
						})
					}
				})
			}
		})
	}
}

func TestServerTransactionRespondReliableCleanup(t *testing.T) {
	const transactions = 20000
	for _, transport := range []string{"TCP", "TLS", "WS", "WSS"} {
		t.Run(transport, func(t *testing.T) {
			for _, status := range []int{StatusOK, StatusBadRequest} {
				t.Run(strconv.Itoa(status), func(t *testing.T) {
					conn := &serverResponseConn{}
					var failures atomic.Int64
					var wg sync.WaitGroup
					for i := 0; i < transactions; i++ {
						tx := newServerResponseTx(t, "BYE", transport, conn)
						wg.Add(1)
						go func() {
							defer wg.Done()
							if err := tx.Respond(NewResponseFromRequest(tx.Origin(), status, strconv.Itoa(status), nil)); err != nil {
								failures.Add(1)
							}
							<-tx.Done()
						}()
					}
					wg.Wait()
					require.EqualValues(t, transactions, conn.writes.Load())
					require.Zero(t, failures.Load(), "successful responses must survive Timer J cleanup")
				})
			}
		})
	}
}
