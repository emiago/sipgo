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

func TestClientTransactionInviteFSM(t *testing.T) {
	// make things fast
	SetTimers(1*time.Millisecond, 1*time.Millisecond, 1*time.Millisecond)
	req, _, _ := testCreateInvite(t, "127.0.0.99:5060", "udp", "127.0.0.2:5060")

	incoming := bytes.NewBuffer([]byte{})
	outgoing := bytes.NewBuffer([]byte{})
	conn := &UDPConnection{
		PacketConn: &fakes.UDPConn{
			Reader:  incoming,
			Writers: map[string]io.Writer{"127.0.0.99:5060": outgoing},
		},
	}
	tx := NewClientTx("123", req, conn, log.Logger)

	// CALLING STATE
	err := tx.Init()
	require.NoError(t, err)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateCalling))

	// PROCEEDING STATE
	res100 := NewResponseFromRequest(req, StatusTrying, "Trying", nil)

	go func() { <-tx.Responses() }() // this will now block our transaction until termination or 200 is consumed
	tx.receive(res100)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateProcceeding))

	// ACCEPTING STATE RFC 6026 or not allowing 2xx to kill transaction because it
	// in case of proxy more 2xx still may need to be retransmitted as part of transaction and avoid dying imediately
	// make timer M small

	res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)
	go func() { <-tx.Responses() }()
	tx.receive(res200)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateAccepted))

	// COMPLETED STATE
	time.Sleep(Timer_M * 2)
	require.NoError(t, compareFunctions(tx.currentFsmState(), tx.inviteStateTerminated))

	// res200 := NewResponseFromRequest(req, StatusOK, "OK", nil)
	// incoming.WriteString(res200.String())

}
