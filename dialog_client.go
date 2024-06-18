package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

type DialogClient struct {
	c          *Client
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
}

func (s *DialogClient) dialogsLen() int {
	leftItems := 0
	s.dialogs.Range(func(key, value any) bool {
		leftItems++
		return true
	})
	return leftItems
}

func (s *DialogClient) loadDialog(id string) *DialogClientSession {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogClientSession)
	return t
}

func (s *DialogClient) MatchRequestDialog(req *sip.Request) (*DialogClientSession, error) {
	return s.matchDialogRequest(req)
}

func (s *DialogClient) matchDialogRequest(req *sip.Request) (*DialogClientSession, error) {
	id, err := sip.UACReadRequestDialogID(req)
	if err != nil {
		return nil, errors.Join(err, ErrDialogOutsideDialog)
	}

	dt := s.loadDialog(id)
	if dt == nil {
		return nil, ErrDialogDoesNotExists
	}
	return dt, nil
}

// NewDialogClient provides handle for managing UAC dialog
// Contact hdr is default to be provided for correct invite. It is not used if you provided hdr as part of request,
// but contact hdr must be present so this makes sure correct dialog is established.
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

// Invite sends INVITE request and creates early dialog session.
// This is actually not yet dialog (ID is empty)
// You need to call WaitAnswer after for establishing dialog
// For passing custom Invite request use WriteInvite
func (c *DialogClient) Invite(ctx context.Context, recipient sip.Uri, body []byte, headers ...sip.Header) (*DialogClientSession, error) {
	req := sip.NewRequest(sip.INVITE, recipient)
	if body != nil {
		req.SetBody(body)
	}

	for _, h := range headers {
		req.AppendHeader(h)
	}
	return c.WriteInvite(ctx, req)
}

func (c *DialogClient) WriteInvite(ctx context.Context, inviteRequest *sip.Request) (*DialogClientSession, error) {
	cli := c.c

	if inviteRequest.Contact() == nil {
		// Set contact only if not exists
		inviteRequest.AppendHeader(&c.contactHDR)
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
		dc:       c,
		inviteTx: tx,
	}

	return dtx, nil
}

func (c *DialogClient) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	dt, err := c.matchDialogRequest(req)
	if err != nil {
		return err
	}

	dt.setState(sip.DialogStateEnded)

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}
	defer dt.Close()              // Delete our dialog always
	defer dt.inviteTx.Terminate() // Terminates Invite transaction

	// select {
	// case <-tx.Done():
	// 	return tx.Err()
	// }
	return nil
}

// Experiment
// ReadRefer reads REFER (Transfer action) and parses referURI if dialog exists.
// Returned dialog you should use to pass NOTIFY and BYE if your new INVITE dialog is successful
func (c *DialogClient) ReadRefer(req *sip.Request, tx sip.ServerTransaction, referUri *sip.Uri) (*DialogClientSession, error) {
	dt, err := c.matchDialogRequest(req)
	if err != nil {
		return nil, err
	}

	cseq := req.CSeq().SeqNo
	if cseq <= dt.lastCSeqNo {
		return nil, ErrDialogOutsideDialog
	}

	referToHdr := req.GetHeader("Refer-to")
	if referToHdr == nil {
		return nil, fmt.Errorf("no Refer-to header present")
	}

	if err := sip.ParseUri(referToHdr.Value(), referUri); err != nil {
		return nil, err
	}

	res := sip.NewResponseFromRequest(req, 202, "Accepted", nil)
	if err := tx.Respond(res); err != nil {
		return nil, err
	}

	// Now dialog should do invite
	// And implicit subscription should be done
	// invite := sip.NewRequest(sip.INVITE, *referUri)
	// invite.SetBody(dt.InviteRequest.Body())
	// invite

	// // dt.TransactionRequest(context.TODO(), invite)
	// c.WriteInvite(context.TODO(), invite)

	return dt, nil

	// Dial until current dialog is canceled. Therefore we pass dt.Context
	// ctx, cancel := context.WithTimeout(dt.Context(), 30*time.Second)
	// defer cancel()

	// c.Invite(ctx, referUri)

	// dt.setState(sip.DialogStateEnded)

	// res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	// if err := tx.Respond(res); err != nil {
	// 	return err
	// }
	// defer dt.Close()              // Delete our dialog always
	// defer dt.inviteTx.Terminate() // Terminates Invite transaction

	// // select {
	// // case <-tx.Done():
	// // 	return tx.Err()
	// // }
	// return nil
}

type DialogClientSession struct {
	Dialog
	dc       *DialogClient
	inviteTx sip.ClientTransaction
}

// TransactionRequest is doing client DIALOG request based on RFC
// https://www.rfc-editor.org/rfc/rfc3261#section-12.2.1
// This ensures that you have proper request done within dialog. You should avoid setting any Dialog header (cseq, from, to, callid)
func (s *DialogClientSession) TransactionRequest(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	cseq := req.CSeq()
	if cseq == nil {
		cseq = &sip.CSeqHeader{
			SeqNo:      s.InviteRequest.CSeq().SeqNo,
			MethodName: req.Method,
		}
		req.AppendHeader(cseq)
	}

	// For safety make sure we are starting with our last dialog cseq num
	cseq.SeqNo = s.lastCSeqNo

	if !req.IsAck() && !req.IsCancel() {
		// Do cseq increment within dialog
		cseq.SeqNo = s.lastCSeqNo + 1
	}

	// Check record route header
	if s.InviteResponse != nil {
		cont := s.InviteResponse.Contact()
		if cont != nil {
			req.Recipient = cont.Address
		}

		if rr := s.InviteResponse.RecordRoute(); rr != nil {
			req.SetDestination(rr.Address.HostPort())
		}
	}

	s.lastCSeqNo = cseq.SeqNo

	// Keep any request inside dialog
	if h, invH := req.From(), s.InviteRequest.From(); h == nil {
		req.AppendHeader(sip.HeaderClone(invH))
	}

	if h, invH := req.To(), s.InviteResponse.To(); h == nil && invH != nil {
		req.AppendHeader(sip.HeaderClone(invH))
	}

	if h, invH := req.CallID(), s.InviteRequest.CallID(); h == nil {
		req.AppendHeader(sip.HeaderClone(invH))
	}

	// Passing option to avoid CSEQ apply
	return s.dc.c.TransactionRequest(ctx, req, ClientRequestBuild)
}

func (s *DialogClientSession) WriteRequest(req *sip.Request) error {
	// Check Record-Route Header
	if s.InviteResponse != nil {
		// Record Route handling
		if rr := s.InviteResponse.RecordRoute(); rr != nil {
			req.SetDestination(rr.Address.HostPort())
		}
	}
	return s.dc.c.WriteRequest(req)
}

// Close must be always called in order to cleanup some internal resources
// Consider that this will not send BYE or CANCEL or change dialog state
func (s *DialogClientSession) Close() error {
	s.dc.dialogs.Delete(s.ID)
	// s.setState(sip.DialogStateEnded)
	// ctx, _ := context.WithTimeout(context.Background(), sip.Timer_B)
	// return s.Bye(ctx)
	return nil
}

type AnswerOptions struct {
	OnResponse func(res *sip.Response)

	// For digest authentication
	Username string
	Password string
}

// WaitAnswer waits for success response or returns ErrDialogResponse in case non 2xx
// Canceling context while waiting 2xx will send Cancel request
// Returns errors:
// - ErrDialogResponse in case non 2xx response
// - any internal in case waiting answer failed for different reasons
func (s *DialogClientSession) WaitAnswer(ctx context.Context, opts AnswerOptions) error {
	tx, inviteRequest := s.inviteTx, s.InviteRequest

	var r *sip.Response
	var err error
	for {
		select {
		case r = <-tx.Responses():
			// just pass
		case <-ctx.Done():
			// Send cancel
			defer tx.Terminate()
			if err := tx.Cancel(); err != nil {
				return errors.Join(err, ctx.Err())
			}
			return ctx.Err()

		case <-tx.Done():
			// tx.Err() can be empty
			return errors.Join(fmt.Errorf("transaction terminated"), tx.Err())
		}

		if opts.OnResponse != nil {
			opts.OnResponse(r)
		}

		s.InviteResponse = r

		if r.IsSuccess() {
			break
		}

		if r.IsProvisional() {
			continue
		}

		if (r.StatusCode == sip.StatusProxyAuthRequired) && opts.Password != "" {
			h := r.GetHeader("Proxy-Authorization")
			if h == nil {
				tx.Terminate()

				digopts := digest.Options{
					Method:   sip.INVITE.String(),
					URI:      inviteRequest.Recipient.Addr(),
					Username: opts.Username,
					Password: opts.Password,
				}

				// First build this request
				if err := digestProxyAuthApply(inviteRequest, r, digopts); err != nil {
					return err
				}

				// Remove Via from original request and send it through dialog transaction
				// This keeps transaction within dialog
				inviteRequest.RemoveHeader("Via")
				tx, err = s.TransactionRequest(ctx, inviteRequest)
				if err != nil {
					return err
				}
				continue
			}
		}

		if r.StatusCode == sip.StatusUnauthorized && opts.Password != "" {
			h := inviteRequest.GetHeader("Authorization")
			if h == nil {
				tx.Terminate()

				digopts := digest.Options{
					Method:   sip.INVITE.String(),
					URI:      inviteRequest.Recipient.Addr(),
					Username: opts.Username,
					Password: opts.Password,
				}

				// First build this request
				if err := digestAuthApply(inviteRequest, r, digopts); err != nil {
					return err
				}

				// Remove Via from original request and send it through dialog transaction
				// This keeps transaction within dialog
				inviteRequest.RemoveHeader("Via")
				tx, err = s.TransactionRequest(ctx, inviteRequest)

				if err != nil {
					return err
				}
				continue
			}
		}

		return &ErrDialogResponse{Res: r}
	}

	id, err := sip.MakeDialogIDFromResponse(r)
	if err != nil {
		return err
	}
	s.inviteTx = tx
	s.InviteResponse = r
	s.ID = id
	s.setState(sip.DialogStateEstablished)
	s.dc.dialogs.Store(id, s)
	return nil
}

// Ack sends ack. Use WriteAck for more customizing
func (s *DialogClientSession) Ack(ctx context.Context) error {
	ack := sip.NewAckRequest(s.InviteRequest, s.InviteResponse, nil)
	return s.WriteAck(ctx, ack)
}

func (s *DialogClientSession) WriteAck(ctx context.Context, ack *sip.Request) error {
	if err := s.WriteRequest(ack); err != nil {
		// Make sure we close our error
		// s.Close()
		return err
	}
	s.setState(sip.DialogStateConfirmed)
	return nil
}

// Bye sends bye and terminates session. Use WriteBye if you want to customize bye request
func (s *DialogClientSession) Bye(ctx context.Context) error {
	bye := newByeRequestUAC(s.InviteRequest, s.InviteResponse, nil)
	return s.WriteBye(ctx, bye)
}

func (s *DialogClientSession) WriteBye(ctx context.Context, bye *sip.Request) error {
	defer s.Close()

	state := s.state.Load()
	// In case dialog terminated
	if sip.DialogState(state) == sip.DialogStateEnded {
		return nil
	}

	// In case dialog was not updated
	if sip.DialogState(state) != sip.DialogStateConfirmed {
		return fmt.Errorf("Dialog not confirmed. ACK not send?")
	}

	tx, err := s.TransactionRequest(ctx, bye)
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
		s.setState(sip.DialogStateEnded)
		return nil
	case <-tx.Done():
		return tx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// newByeRequestUAC creates bye request from established dialog
// https://datatracker.ietf.org/doc/html/rfc3261#section-15.1.1
// NOTE: it does not copy Via header neither increases CSEQ. This is left to dialog transaction request
func newByeRequestUAC(inviteRequest *sip.Request, inviteResponse *sip.Response, body []byte) *sip.Request {
	recipient := &inviteRequest.Recipient
	cont := inviteResponse.Contact()
	if cont != nil {
		// BYE is subsequent request
		recipient = &cont.Address
	}

	byeRequest := sip.NewRequest(
		sip.BYE,
		*recipient.Clone(),
	)
	byeRequest.SipVersion = inviteRequest.SipVersion

	if len(inviteRequest.GetHeaders("Route")) > 0 {
		sip.CopyHeaders("Route", inviteRequest, byeRequest)
	}

	maxForwardsHeader := sip.MaxForwardsHeader(70)
	byeRequest.AppendHeader(&maxForwardsHeader)
	if h := inviteRequest.From(); h != nil {
		byeRequest.AppendHeader(sip.HeaderClone(h))
	}

	if h := inviteResponse.To(); h != nil {
		byeRequest.AppendHeader(sip.HeaderClone(h))
	}

	if h := inviteRequest.CallID(); h != nil {
		byeRequest.AppendHeader(sip.HeaderClone(h))
	}

	if h := inviteRequest.CSeq(); h != nil {
		byeRequest.AppendHeader(sip.HeaderClone(h))
	}

	cseq := byeRequest.CSeq()
	cseq.MethodName = sip.BYE

	byeRequest.SetBody(body)
	byeRequest.SetTransport(inviteRequest.Transport())
	byeRequest.SetSource(inviteRequest.Source())
	return byeRequest
}
