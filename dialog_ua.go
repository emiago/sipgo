package sipgo

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
	uuid "github.com/satori/go.uuid"
)

// DialogUA defines UserAgent that will be used in controling your dialog.
type DialogUA struct {
	Client     *Client
	ContactHDR sip.ContactHeader
}

func (c *DialogUA) ReadInvite(inviteReq *sip.Request, tx sip.ServerTransaction) (*DialogServerSession, error) {
	cont := inviteReq.Contact()
	if cont == nil {
		return nil, ErrDialogInviteNoContact
	}
	// Prebuild already to tag for response as it must be same for all responds
	// NewResponseFromRequest will skip this for all 100
	uuid, err := uuid.NewV4()
	if err != nil {
		return nil, fmt.Errorf("generating dialog to tag failed: %w", err)
	}
	inviteReq.To().Params["tag"] = uuid.String()
	id, err := sip.UASReadRequestDialogID(inviteReq)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	dtx := &DialogServerSession{
		Dialog: Dialog{
			ID:            id, // this id has already prebuilt tag
			InviteRequest: inviteReq,
			lastCSeqNo:    inviteReq.CSeq().SeqNo,
			state:         atomic.Int32{},
			stateCh:       make(chan sip.DialogState, 3),
			ctx:           ctx,
			cancel:        cancel,
		},
		inviteTx: tx,
		ua:       c,
	}
	return dtx, nil
}

func (ua *DialogUA) Invite(ctx context.Context, recipient sip.Uri, body []byte, headers ...sip.Header) (*DialogClientSession, error) {
	req := sip.NewRequest(sip.INVITE, recipient)
	if body != nil {
		req.SetBody(body)
	}

	for _, h := range headers {
		req.AppendHeader(h)
	}
	return ua.WriteInvite(ctx, req)
}

func (c *DialogUA) WriteInvite(ctx context.Context, inviteRequest *sip.Request) (*DialogClientSession, error) {
	cli := c.Client

	if inviteRequest.Contact() == nil {
		// Set contact only if not exists
		inviteRequest.AppendHeader(&c.ContactHDR)
	}

	tx, err := cli.TransactionRequest(ctx, inviteRequest)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	dtx := &DialogClientSession{
		Dialog: Dialog{
			InviteRequest: inviteRequest,
			lastCSeqNo:    inviteRequest.CSeq().SeqNo,
			state:         atomic.Int32{},
			stateCh:       make(chan sip.DialogState, 3),
			ctx:           ctx,
			cancel:        cancel,
		},
		// dc:       c,
		ua:       c,
		inviteTx: tx,
	}

	return dtx, nil
}
