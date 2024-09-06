package sipgo

import (
	"context"
	"fmt"

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

	dtx := &DialogServerSession{
		Dialog: Dialog{
			ID:            id, // this id has already prebuilt tag
			InviteRequest: inviteReq,
		},
		inviteTx: tx,
		ua:       c,
	}
	dtx.Init()

	// Temporarly fix
	if stx, ok := tx.(*sip.ServerTx); ok {
		stx.OnTerminate(func(key string) {
			state := dtx.LoadState()
			if state < sip.DialogStateEstablished {
				// It is canceled
				dtx.setState(sip.DialogStateEnded)
			}
		})
		// stx.OnCancel(func(r *sip.Request) {
		// 	dtx.setState(sip.DialogStateEnded)
		// })
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

func (c *DialogUA) WriteInvite(ctx context.Context, inviteReq *sip.Request) (*DialogClientSession, error) {
	cli := c.Client

	if inviteReq.Contact() == nil {
		// Set contact only if not exists
		inviteReq.AppendHeader(&c.ContactHDR)
	}

	tx, err := cli.TransactionRequest(ctx, inviteReq)
	if err != nil {
		return nil, err
	}

	dtx := &DialogClientSession{
		Dialog: Dialog{
			InviteRequest: inviteReq,
		},
		ua:       c,
		inviteTx: tx,
	}
	dtx.Init()

	return dtx, nil
}
