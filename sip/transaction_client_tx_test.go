package sip

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

func TestClientTransactionInviteFSM(t *testing.T) {
	// make things fast
	SetTimers(1*time.Millisecond, 1*time.Millisecond, 1*time.Millisecond)
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")
	req.raddr = Addr{IP: net.ParseIP("127.0.0.99"), Port: 5060}

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})
	conn := &UDPConnection{
		PacketConn: &fakes.UDPConn{
			Reader:  incoming,
			Writers: map[string]io.Writer{"127.0.0.99:5060": outgoing},
		},
	}
	tx := NewClientTx("123", req, conn, slog.Default())

	err := tx.Init()
	require.NoError(t, err)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateCalling))

	// PROCEEDING STATE
	res100 := NewResponseFromRequest(req, StatusTrying, "Trying", nil)

	go func() { <-tx.Responses() }() // this will now block our transaction until termination or 200 is consumed
	tx.Receive(res100)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateProcceeding))

	// ACCEPTING STATE RFC 6026 or not allowing 2xx to kill transaction because it
	// in case of proxy more 2xx still may need to be retransmitted as part of transaction and avoid dying imediately
	// make timer M small

	res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)
	go func() { <-tx.Responses() }()
	tx.Receive(res200)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateAccepted))

	// COMPLETED STATE
	time.Sleep(Timer_M * 2)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateTerminated))

	// res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)
	// incoming.WriteString(res200.String())

}

func TestClientTransactionFSM(t *testing.T) {
	// SetTimers(1*time.Millisecond, 1*time.Millisecond, 1*time.Millisecond)
	req, _, _ := testCreateInvite(t, "sip:127.0.0.99:5060", "udp", "127.0.0.2:5060")

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})

	t.Run("PassUpResponse", func(t *testing.T) {
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.99:5060": outgoing},
			},
		}
		tx := NewClientTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		res100 := NewResponseFromRequest(req, StatusTrying, "Trying", nil)
		res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)

		go func() {
			tx.Receive(res100)
			tx.Receive(res200)
		}()

		// This is racy
		passUp100 := <-tx.Responses()
		passUp200 := <-tx.Responses()

		require.Equal(t, res100.StartLine(), passUp100.StartLine())
		require.Equal(t, res200.StartLine(), passUp200.StartLine())
	})

	t.Run("OutOfOrderResponse", func(t *testing.T) {
		conn := &UDPConnection{
			PacketConn: &fakes.UDPConn{
				Reader:  incoming,
				Writers: map[string]io.Writer{"127.0.0.99:5060": outgoing},
			},
		}
		tx := NewClientTx("123", req, conn, slog.Default())
		err := tx.Init()
		require.NoError(t, err)

		res100 := NewResponseFromRequest(req, StatusTrying, "Trying", nil)
		res180 := NewResponseFromRequest(req, StatusRinging, "Ringing", nil)
		res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)

		wg := &sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// We received first 200 and then 100
			tx.Receive(res180)
			tx.Receive(res200)
			tx.Receive(res100)
			tx.Receive(res100)
		}()

		passUp180 := <-tx.Responses()
		require.Equal(t, res180.StartLine(), passUp180.StartLine())

		passUp200 := <-tx.Responses()
		require.Equal(t, res200.StartLine(), passUp200.StartLine())

		wg.Wait()
		// State should not change from answered
		require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateAccepted))
	})
}
