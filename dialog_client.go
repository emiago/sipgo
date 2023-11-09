package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

type DialogClient struct {
	c          *Client
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
}

func (s *DialogClient) loadDialog(id string) *DialogClientSession {
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
	Res *sip.Response
}

func (e ErrDialogResponse) Error() string {
	return fmt.Sprintf("Invite failed with response: %s", e.Res.StartLine())
}

// Invite sends INVITE request and waits for success response or returns ErrDialogResponse in case non 2xx
// Canceling context while waiting 2xx will send Cancel request
// For more customizing Invite request use WriteInvite instead
func (dc *DialogClient) Invite(ctx context.Context, recipient *sip.Uri, body []byte, headers ...sip.Header) (*DialogClientSession, error) {
	req := sip.NewRequest(sip.INVITE, recipient)
	if body != nil {
		req.SetBody(body)
	}

	for _, h := range headers {
		req.AppendHeader(h)
	}
	return dc.WriteInvite(ctx, req, InviteOptions{})
}

type InviteOptions struct {
	OnResponse func(res *sip.Response)

	// For digest authentication
	Username string
	Password string
}

func (dc *DialogClient) WriteInvite(ctx context.Context, inviteRequest *sip.Request, opts InviteOptions) (*DialogClientSession, error) {
	cli := dc.c

	inviteRequest.AppendHeader(&dc.contactHDR)

	// TODO passing client transaction options is now hidden
	tx, err := cli.TransactionRequest(ctx, inviteRequest)
	if err != nil {
		return nil, err
	}

	var r *sip.Response
	for {
		select {
		case r = <-tx.Responses():
			// just pass
		case <-ctx.Done():
			// Send cancel
			defer tx.Terminate()
			if err := tx.Cancel(); err != nil {
				return nil, errors.Join(err, ctx.Err())
			}
			return nil, ctx.Err()

		case <-tx.Done():
			return nil, tx.Err()
		}

		if opts.OnResponse != nil {
			opts.OnResponse(r)
		}

		if r.IsSuccess() {
			break
		}

		if r.IsProvisional() {
			continue
		}

		if r.StatusCode == sip.StatusUnauthorized && opts.Password != "" {
			h := inviteRequest.GetHeader("Authorization")
			if h == nil {
				tx.Terminate()
				tx, err = digestTransactionRequest(ctx, dc.c, inviteRequest, r, digest.Options{
					Method:   sip.INVITE.String(),
					Username: opts.Username,
					Password: opts.Password,
				})
				if err != nil {
					return nil, err
				}
			}

			continue
		}

		return nil, &ErrDialogResponse{Res: r}
	}

	id, err := sip.MakeDialogIDFromResponse(r)
	if err != nil {
		return nil, err
	}

	d := Dialog{
		ID:             id,
		InviteRequest:  inviteRequest,
		InviteResponse: r,
		state:          sip.DialogStateEstablished,
	}

	dtx := &DialogClientSession{
		Dialog:   d,
		dc:       dc,
		inviteTx: tx,
	}

	dc.dialogs.Store(id, dtx)
	return dtx, nil
}

func (dc *DialogClient) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	callid, _ := req.CallID()
	from, _ := req.From()
	to, _ := req.To()

	id := sip.MakeDialogID(callid.Value(), from.Params["tag"], to.Params["tag"])

	dt := dc.loadDialog(id)
	if dt == nil {
		return ErrDialogDoesNotExists
	}

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}
	dt.inviteTx.Terminate() // Terminates Invite transaction

	// select {
	// case <-tx.Done():
	// 	return tx.Err()
	// }
	return nil
}

type DialogClientSession struct {
	Dialog
	dc       *DialogClient
	inviteTx sip.ClientTransaction
}

// Ack sends ack. Use WriteAck for more customizing
func (s *DialogClientSession) Ack(ctx context.Context) error {
	ack := sip.NewAckRequest(s.InviteRequest, s.InviteResponse, nil)
	return s.WriteAck(ctx, ack)
}

func (s *DialogClientSession) WriteAck(ctx context.Context, ack *sip.Request) error {
	if err := s.dc.c.WriteRequest(ack); err != nil {
		return err
	}
	s.Dialog.state = sip.DialogStateConfirmed
	return nil
}

// Bye sends bye and terminates session. Use WriteBye if you want to customize bye request
func (s *DialogClientSession) Bye(ctx context.Context) error {
	bye := sip.NewByeRequestUAC(s.InviteRequest, s.InviteResponse, nil)
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
		s.Dialog.state = sip.DialogStateConfirmed
		return nil
	case <-tx.Done():
		return tx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *DialogClientSession) Done() <-chan struct{} {
	return s.inviteTx.Done()
}

// digestTransactionRequest checks response if 401 and sends digest auth
func digestTransactionRequest(ctx context.Context, client *Client, req *sip.Request, res *sip.Response, opts digest.Options) (sip.ClientTransaction, error) {
	// Get WwW-Authenticate
	wwwAuth := res.GetHeader("WWW-Authenticate")
	chal, err := digest.ParseChallenge(wwwAuth.Value())
	if err != nil {
		return nil, fmt.Errorf("fail to parse chalenge wwwauth=%q: %w", wwwAuth.Value(), err)
	}

	// Reply with digest
	cred, err := digest.Digest(chal, opts)
	if err != nil {
		return nil, fmt.Errorf("fail to build digest: %w", err)
	}

	cseq, _ := req.CSeq()
	cseq.SeqNo++
	// newReq := req.Clone()

	req.AppendHeader(sip.NewHeader("Authorization", cred.String()))
	// defer req.RemoveHeader("Authorization")

	req.RemoveHeader("Via")
	tx, err := client.TransactionRequest(context.TODO(), req, ClientRequestAddVia)
	return tx, err
}
