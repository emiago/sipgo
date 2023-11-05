package sipgo

import (
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/siptest"
	"github.com/stretchr/testify/require"
)

func TestDialogServer(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	srv, err := NewServer(ua)
	require.Nil(t, err)
	defer srv.Close()
	contactHDR := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "test.com"},
	}

	dialogSrv := NewDialogServer(contactHDR)

	inviteHandler := func(req *sip.Request, tx sip.ServerTransaction) {
		dtx := dialogSrv.Invite(req, tx)

		err := dtx.WriteResponse(sip.StatusTrying, "Trying", nil)
		require.Nil(t, err)

		err = dtx.WriteResponse(sip.StatusRinging, "Ringing", nil)
		require.Nil(t, err)

		err = dtx.WriteResponse(sip.StatusOK, "OK", nil)
		require.Nil(t, err)

		// <-dtx.Done()
	}

	ackHandler := func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.Ack(req, tx)
	}

	byeHandler := func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.Bye(req, tx)
	}

	// Sending INVITE
	invite, _, _ := createTestInvite(t, "sip:test@test.com", "udp", "127.0.0.1:5060")
	tx := siptest.NewServerTxRecorder(invite)
	inviteHandler(invite, tx)

	resps := tx.Result()
	require.Len(t, resps, 3)
	// Check all headers are present
	for _, r := range resps {
		chdr, _ := r.Contact()
		require.Equal(t, contactHDR, *chdr)
	}

	okResp := resps[2]
	require.Equal(t, sip.StatusOK, okResp.StatusCode)

	// Sending ACK
	ack := sip.NewAckRequest(invite, okResp, nil)
	tx = siptest.NewServerTxRecorder(ack)
	ackHandler(ack, tx)
	// No reponses should be setn
	resps = tx.Result()
	require.Len(t, resps, 0)

	// Sending BYE
	bye := sip.NewByeRequestUAC(invite, okResp, nil)
	tx = siptest.NewServerTxRecorder(bye)
	time.AfterFunc(1*time.Second, func() {
		// Force termination
		// Not to wait Timer_J
		tx.Terminate()
	})
	byeHandler(bye, tx)

	resps = tx.Result()
	require.Len(t, resps, 1)
	require.Equal(t, sip.StatusOK, resps[0].StatusCode)
}
