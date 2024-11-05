package sip

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func TestServerTransactionFSM(t *testing.T) {
	// SetTimers(1*time.Millisecond, 1*time.Millisecond, 1*time.Millisecond)
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})

	t.Run("PassUpResponse", func(t *testing.T) {
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.2:5060": outgoing},
			},
		}
		tx := NewServerTx("123", req, conn, log.Logger)
		err := tx.Init()
		require.NoError(t, err)

		err = tx.Receive(req)
		require.NoError(t, err)
	})

	t.Run("OutOfOrderResponse", func(t *testing.T) {
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.2:5060": outgoing},
			},
		}
		tx := NewServerTx("123", req, conn, log.Logger)
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
		tx := NewServerTx("123", req, conn, log.Logger)
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
