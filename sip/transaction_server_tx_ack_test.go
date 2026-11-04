package sip

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

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
