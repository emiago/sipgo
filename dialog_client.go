package sipgo

import (
	"context"
	"fmt"
	"sync"

	"github.com/emiago/sipgo/sip"
)

type DialogClient struct {
	c          *Client
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
}

func (s *DialogClient) LoadDialog(id string) *DialogClientSession {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogClientSession)
	return t
}

// NewDialogClient provides handle for managing UAC dialog
// Contact hdr must be provided for correct invite
// In case handling different transports you should have multiple instances per transport
func NewDialogClient(client *Client, contactHDR sip.ContactHeader) *DialogClient {
	s := &DialogClient{
		c:          client,
		dialogs:    sync.Map{},
		contactHDR: contactHDR,
	}
	return s
}

type ErrDialogResponse struct {
	r *sip.Response
}

func (e ErrDialogResponse) Error() string {
	return fmt.Sprintf("Invite failed with response: %s", e.r.StartLine())
}

// Invite sends INVITE request and waits for success response or returns ErrDialogResponse in case non 2xx
// Canceling context while waiting 2xx will send Cancel request
// For more customizing Invite request use WriteInvite instead
func (c *DialogClient) Invite(ctx context.Context, recipient *sip.Uri, body []byte, contentTypeHdr sip.ContentTypeHeader) (*DialogClientSession, error) {
	req := sip.NewRequest(sip.INVITE, recipient)
	if body != nil {
		req.SetBody(body)
		req.AppendHeader(&contentTypeHdr)
	}
	return c.WriteInvite(ctx, req)
}

func (c *DialogClient) WriteInvite(ctx context.Context, inviteRequest *sip.Request) (*DialogClientSession, error) {
	cli := c.c

	inviteRequest.AppendHeader(&c.contactHDR)

	// TODO passing client transaction options is now hidden
	tx, err := cli.TransactionRequest(ctx, inviteRequest)
	if err != nil {
		return nil, err
	}

	var r *sip.Response
	for {
		select {
		case r = <-tx.Responses():
		case <-ctx.Done():
			// Send cancel
			defer tx.Terminate()
			return nil, tx.Cancel()

		case <-tx.Done():
			return nil, tx.Err()
		}

		if r.IsProvisional() {
			continue
		}

		if !r.IsSuccess() {
			// Send ACK
			ack := sip.NewAckRequest(inviteRequest, r, nil)
			cli.WriteRequest(ack)

			return nil, &ErrDialogResponse{r: r}
		}

		break
	}

	id, err := sip.MakeDialogIDFromResponse(r)
	if err != nil {
		return nil, err
	}

	d := Dialog{
		ID:       id,
		Invite:   inviteRequest,
		Response: r,
		State:    sip.DialogStateEstablished,
	}

	dtx := &DialogClientSession{
		Dialog:   d,
		dc:       c,
		inviteTx: tx,
	}

	c.dialogs.Store(id, dtx)
	return dtx, nil
}

type DialogClientSession struct {
	Dialog
	dc       *DialogClient
	inviteTx sip.ClientTransaction
}

// Ack sends ack. Use WriteAck for more customizing
func (s *DialogClientSession) Ack(ctx context.Context) error {
	ack := sip.NewAckRequest(s.Invite, s.Response, nil)
	return s.WriteAck(ctx, ack)
}

func (s *DialogClientSession) WriteAck(ctx context.Context, ack *sip.Request) error {
	if err := s.dc.c.WriteRequest(ack); err != nil {
		return err
	}
	s.Dialog.State = sip.DialogStateConfirmed
	return nil
}

// Bye sends bye and terminates session. Use WriteBye if you want to customize bye request
func (s *DialogClientSession) Bye(ctx context.Context) error {
	bye := sip.NewByeRequestUAC(s.Invite, s.Response, nil)
	return s.WriteBye(ctx, bye)
}

func (s *DialogClientSession) WriteBye(ctx context.Context, bye *sip.Request) error {
	dc := s.dc
	defer dc.dialogs.Delete(s.ID) // Delete our dialog always

	tx, err := dc.c.TransactionRequest(ctx, bye)
	if err != nil {
		return err
	}
	defer s.inviteTx.Terminate() // Terminates INVITE in all cases
	defer tx.Terminate()         // Terminates current transaction

	// Wait 200
	select {
	case res := <-tx.Responses():
		if res.StatusCode != 200 {
			return ErrDialogResponse{res}
		}
		s.Dialog.State = sip.DialogStateConfirmed
		return nil
	case <-tx.Done():
		return tx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *DialogClientSession) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		// Non dialog Bye?
		return nil
	}

	dt := s.dc.LoadDialog(id)
	if dt == nil {
		return fmt.Errorf("no existing dialog")
	}
	defer dt.inviteTx.Terminate() // Terminates Invite transaction

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}

	select {
	case <-tx.Done():
		return tx.Err()
	}
}
