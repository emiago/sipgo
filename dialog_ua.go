package sipgo

import (
	"context"
	"fmt"

	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
)

// DialogUA defines UserAgent that will be used in controling your dialog.
// It needs client handle for cancelation or sending more subsequent request during dialog
type DialogUA struct {
	// Client (required) is used to build and send subsequent request (CANCEL, BYE)
	Client *Client
	// ContactHDR (required) is used as default one to build request/response.
	// You can pass custom on each request, but in dialog it is required to be present
	ContactHDR sip.ContactHeader

	// RewriteContact sends request on source IP instead Contact. Should be used when behind NAT.
	RewriteContact bool
}

func (c *DialogUA) ReadInvite(inviteRequest *sip.Request, tx sip.ServerTransaction) (*DialogServerSession, error) {
	// do some minimal validation
	if inviteRequest.Contact() == nil {
		return nil, ErrDialogInviteNoContact
	}
	if inviteRequest.CSeq() == nil {
		return nil, fmt.Errorf("no CSEQ header present")
	}

	// Prebuild already to tag for response as it must be same for all responds
	// NewResponseFromRequest will skip this for all 100
	uuid, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generating dialog to tag failed: %w", err)
	}
	// As we are modifying request we need to perform shallow clone to avoid transaction races
	inviteReq := inviteRequest.Clone()
	inviteReq.To().Params["tag"] = uuid.String()
	id, err := sip.DialogIDFromRequestUAS(inviteReq)
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

	if !tx.OnCancel(func(r *sip.Request) {
		state := dtx.LoadState()
		if state < sip.DialogStateEstablished {
			// It is mostly canceled if transaction died before answer
			// NOTE this only happens if we sent provisional and before final response
			dtx.endWithCause(sip.ErrTransactionCanceled)
		}
	}) {
		if err := tx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("transaction terminated already")
	}

	if !tx.OnTerminate(func(key string, err error) {
		// NOTE: do not call any here tx FSM related functions as they can cause deadlock
		state := dtx.LoadState()
		if state < sip.DialogStateEstablished {
			// It is mostly canceled if transaction died before answer
			// NOTE this only happens if we sent provisional and before final response
			// if err == sip.ErrTransactionCanceled {
			// 	dtx.endWithCause(sip.ErrTransactionCanceled)
			// 	return
			// }
			dtx.endWithCause(nil)
		}
	}) {
		if err := tx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("transaction terminated already")
	}

	return dtx, nil
}

func (ua *DialogUA) Invite(ctx context.Context, recipient sip.Uri, body []byte, headers ...sip.Header) (*DialogClientSession, error) {
	req := sip.NewRequest(sip.INVITE, recipient)
	if body != nil {
		req.SetBody(body)
	}

	if recipient.UriParams != nil {
		if tran, _ := recipient.UriParams.Get("transport"); tran != "" {
			req.SetTransport(tran)
		}
	}

	for _, h := range headers {
		req.AppendHeader(h)
	}
	return ua.WriteInvite(ctx, req)
}

func (c *DialogUA) WriteInvite(ctx context.Context, inviteReq *sip.Request, options ...ClientRequestOption) (*DialogClientSession, error) {
	if inviteReq.Contact() == nil {
		// Set contact only if not exists
		inviteReq.AppendHeader(&c.ContactHDR)
	}

	dtx := &DialogClientSession{
		Dialog: Dialog{
			InviteRequest: inviteReq,
		},
		UA: c,
	}
	// Init our dialog
	dtx.Dialog.Init()

	return dtx, dtx.Invite(ctx, options...)
}
