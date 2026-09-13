package sipgo

import (
	"context"
	"log/slog"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A PRACK or an UPDATE sent between the INVITE and its final response moves the
// dialog sequence number on. The ACK for the 2xx must still carry the sequence
// number of the INVITE it acknowledges, and it must not consume a number of its
// own, so that the next in dialog request stays above the PRACK.
//
// https://datatracker.ietf.org/doc/html/rfc3261#section-13.2.2.4
func TestDialogClientAckCSeqIsInviteCSeq(t *testing.T) {
	client := testClient(t, func(req *sip.Request) *sip.Response {
		return sip.NewResponseFromRequest(req, 200, "OK", nil)
	})

	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "test", Host: "uas.example.com"})
	invite.AppendHeader(sip.NewHeader("Contact", "<sip:uac@uac.example.com>"))
	require.NoError(t, clientRequestBuildReq(client, invite))
	inviteCSeq := invite.CSeq().SeqNo

	res := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.example.com>"))

	s := DialogClientSession{
		UA:       &DialogUA{Client: client},
		Dialog:   Dialog{InviteRequest: invite, InviteResponse: res},
		inviteTx: sip.NewClientTx("test", invite, nil, slog.Default()),
	}
	s.Dialog.Init()

	ctx := context.Background()

	// Two reliable provisional responses acknowledged with PRACK. Each PRACK is
	// a request of its own within the dialog and takes the next number.
	for i := uint32(1); i <= 2; i++ {
		prack := sip.NewRequest(sip.PRACK, res.Contact().Address)
		_, err := s.TransactionRequest(ctx, prack)
		require.NoError(t, err)
		assert.Equal(t, inviteCSeq+i, prack.CSeq().SeqNo, "PRACK takes the next number in the dialog")
	}

	// The write itself has no transport in this test, only the request built for
	// it is of interest.
	ack := newAckRequestUAC(s.InviteRequest, s.InviteResponse, nil)
	_ = s.WriteAck(ctx, ack)
	assert.Equal(t, inviteCSeq, ack.CSeq().SeqNo, "ACK carries the CSeq of the INVITE it acknowledges")
	assert.Equal(t, sip.ACK, ack.CSeq().MethodName)

	// The ACK consumed no number of its own, so the next request continues above
	// the last PRACK instead of colliding with it.
	bye := newByeRequestUAC(s.InviteRequest, s.InviteResponse, nil)
	_, err := s.TransactionRequest(ctx, bye)
	require.NoError(t, err)
	assert.Equal(t, inviteCSeq+3, bye.CSeq().SeqNo, "BYE continues above the last PRACK")
}

// An ACK for a re-INVITE cannot use the CSeq of the initial INVITE: the caller
// knows which transaction is being acknowledged and sets the header itself.
func TestDialogClientAckCSeqKeptWhenSet(t *testing.T) {
	client := testClient(t, func(req *sip.Request) *sip.Response {
		return sip.NewResponseFromRequest(req, 200, "OK", nil)
	})

	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "test", Host: "uas.example.com"})
	invite.AppendHeader(sip.NewHeader("Contact", "<sip:uac@uac.example.com>"))
	require.NoError(t, clientRequestBuildReq(client, invite))
	inviteCSeq := invite.CSeq().SeqNo

	res := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.example.com>"))

	s := DialogClientSession{
		UA:       &DialogUA{Client: client},
		Dialog:   Dialog{InviteRequest: invite, InviteResponse: res},
		inviteTx: sip.NewClientTx("test", invite, nil, slog.Default()),
	}
	s.Dialog.Init()

	ctx := context.Background()

	reinvite := sip.NewRequest(sip.INVITE, res.Contact().Address)
	_, err := s.TransactionRequest(ctx, reinvite)
	require.NoError(t, err)
	require.Equal(t, inviteCSeq+1, reinvite.CSeq().SeqNo)

	ack := sip.NewRequest(sip.ACK, res.Contact().Address)
	ack.AppendHeader(&sip.CSeqHeader{SeqNo: reinvite.CSeq().SeqNo, MethodName: sip.ACK})
	_ = s.WriteAck(ctx, ack)
	assert.Equal(t, reinvite.CSeq().SeqNo, ack.CSeq().SeqNo, "ACK for a re-INVITE keeps the CSeq set by the caller")
}

// An ACK for a re-INVITE built without a CSeq header must acknowledge that
// re-INVITE, not the initial one: WriteAck does not ask the caller to set the
// header, and the sequence number of the initial INVITE would leave the UAS
// retransmitting its 2xx.
func TestDialogClientAckCSeqReInviteWithoutHeader(t *testing.T) {
	client := testClient(t, func(req *sip.Request) *sip.Response {
		return sip.NewResponseFromRequest(req, 200, "OK", nil)
	})

	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "test", Host: "uas.example.com"})
	invite.AppendHeader(sip.NewHeader("Contact", "<sip:uac@uac.example.com>"))
	require.NoError(t, clientRequestBuildReq(client, invite))
	inviteCSeq := invite.CSeq().SeqNo

	res := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.example.com>"))

	s := DialogClientSession{
		UA:       &DialogUA{Client: client},
		Dialog:   Dialog{InviteRequest: invite, InviteResponse: res},
		inviteTx: sip.NewClientTx("test", invite, nil, slog.Default()),
	}
	s.Dialog.Init()

	ctx := context.Background()

	reinvite := sip.NewRequest(sip.INVITE, res.Contact().Address)
	_, err := s.TransactionRequest(ctx, reinvite)
	require.NoError(t, err)
	require.Equal(t, inviteCSeq+1, reinvite.CSeq().SeqNo)

	ack := sip.NewRequest(sip.ACK, res.Contact().Address)
	_ = s.WriteAck(ctx, ack)
	assert.Equal(t, reinvite.CSeq().SeqNo, ack.CSeq().SeqNo,
		"ACK without a CSeq header acknowledges the last INVITE sent")
	assert.Equal(t, sip.ACK, ack.CSeq().MethodName)
}
