package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

type DialogClientSession struct {
	Dialog
	// dc       *DialogClient
	inviteTx sip.ClientTransaction
	ua       *DialogUA

	// onClose triggers when user calls Close
	onClose func()
}

func (dt *DialogClientSession) validateRequest(req *sip.Request) (err error) {
	// Make sure this is bye for this dialog
	if req.CSeq().SeqNo < dt.lastCSeqNo.Load() {
		return ErrDialogInvalidCseq
	}
	return nil
}

func (s *DialogClientSession) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	s.setState(sip.DialogStateEnded)

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}
	defer s.Close()              // Delete our dialog always
	defer s.inviteTx.Terminate() // Terminates Invite transaction

	// select {
	// case <-tx.Done():
	// 	return tx.Err()
	// }
	return nil
}

// ReadRequest is generic func to validate new request in dialog and update seq. Use it if there are no predefined
func (s *DialogClientSession) ReadRequest(req *sip.Request, tx sip.ServerTransaction) error {
	if err := s.validateRequest(req); err != nil {
		return err
	}

	s.lastCSeqNo.Store(req.CSeq().SeqNo)
	return nil
}

// Do sends request and waits final response using Dialog rules
// For more control use TransactionRequest
//
// NOTE:
// It does not provide INVITE CANCEL as it could be REINVITE
// Use WaitAnswer when creating initial INVITE to have CANCEL sending.
func (s *DialogClientSession) Do(ctx context.Context, req *sip.Request) (*sip.Response, error) {
	tx, err := s.TransactionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer tx.Terminate()
	for {
		select {
		case res := <-tx.Responses():
			if res.IsProvisional() {
				continue
			}
			return res, nil

		case <-tx.Done():
			return nil, tx.Err()

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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
	cseq.SeqNo = s.lastCSeqNo.Load()

	if !req.IsAck() && !req.IsCancel() {
		// Do cseq increment within dialog
		cseq.SeqNo++
	}

	// Check record route header
	if s.InviteResponse != nil {
		hdrs := s.InviteResponse.GetHeaders("record-route")
		if len(hdrs) > 0 {
			for i := len(hdrs) - 1; i >= 0; i-- {
				// We need to put record-route as recipient in case of strict routing
				recordRoute := hdrs[i]
				req.AppendHeader(sip.NewHeader("Route", recordRoute.Value()))
			}

			// Now check top most route header with lazy header parsing
			rh := req.Route()
			if !rh.Address.UriParams.Has("lr") {
				// this is strict routing
				req.Recipient = rh.Address
			}
		}
	}

	s.lastCSeqNo.Store(cseq.SeqNo)
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
	return s.ua.Client.TransactionRequest(ctx, req, ClientRequestBuild)
}

func (s *DialogClientSession) WriteRequest(req *sip.Request) error {
	// Check Record-Route Header
	if s.InviteResponse != nil {
		// Record Route handling
		if rr := s.InviteResponse.RecordRoute(); rr != nil {
			req.SetDestination(rr.Address.HostPort())
		}
	}
	return s.ua.Client.WriteRequest(req)
}

// Close must be always called in order to cleanup some internal resources
// Consider that this will not send BYE or CANCEL or change dialog state
func (s *DialogClientSession) Close() error {
	if s.onClose != nil {
		s.onClose()
	}
	// s.ua.dialogs.Delete(s.ID)
	// s.setState(sip.DialogStateEnded)
	// ctx, _ := context.WithTimeout(context.Background(), sip.Timer_B)
	// return s.Bye(ctx)
	return nil
}

type AnswerOptions struct {
	OnResponse func(res *sip.Response) error

	// For digest authentication
	Username string
	Password string
}

var (
	WaitAnswerForceCancelErr = errors.New("Context cancel forced")
)

// WaitAnswer waits for success response or returns ErrDialogResponse in case non 2xx
// Canceling context while waiting 2xx will send Cancel request. It will block until 1xx provisional is not received
// If Canceling succesfull context.Canceled error is returned
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
			s.InviteResponse = r
			// just pass
		case <-ctx.Done():
			// Send cancel
			// https://datatracker.ietf.org/doc/html/rfc3261#section-9.1
			// Cancel can only be sent when provisional is received
			// We will wait until transaction timeous out (TimerB)
			defer tx.Terminate()

			if err := context.Cause(ctx); err == WaitAnswerForceCancelErr {
				// In case caller wants to force cancelation exit.
				return ctx.Err()
			}

			if s.InviteResponse == nil {
				select {
				case r = <-tx.Responses():
					s.InviteResponse = r
					if !r.IsProvisional() {
						// Maybe consider sending BYE
						return fmt.Errorf("non provisional response received during CANCEL. resp=%s", r.String())
					}
				case <-tx.Done():
					return errors.Join(fmt.Errorf("transaction terminated"), tx.Err())
				}
			}

			cancelReq := newCancelRequest(s.InviteRequest)
			res, err := s.Do(context.Background(), cancelReq) // Cancel should grab same connection underhood
			if err != nil {
				return err
			}
			if res.StatusCode != 200 {
				return fmt.Errorf("cancel failed with non 200. code=%d", res.StatusCode)
			}

			// Wait for 487 or just timeout
			// https://datatracker.ietf.org/doc/html/rfc3261#section-9.1
			// UAC canceling a request cannot rely on receiving a 487 (Request
			// Terminated) response for the original request, as an RFC 2543-
			// compliant UAS will not generate such a response.  If there is no
			// final response for the original request in 64*T1 seconds
		loop_487:
			for {
				select {
				case r = <-tx.Responses():
					if r.IsProvisional() {
						continue
					}
					s.InviteResponse = r
					break loop_487
				case <-tx.Done():
					return tx.Err()
				case <-time.After(64 * sip.T1):
					break loop_487
				}
			}

			return ctx.Err()
		case <-tx.Done():
			// tx.Err() can be empty
			return errors.Join(fmt.Errorf("transaction terminated"), tx.Err())
		}

		if opts.OnResponse != nil {
			if err := opts.OnResponse(r); err != nil {
				return err
			}
		}

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

func newCancelRequest(inviteRequest *sip.Request) *sip.Request {
	cancelReq := sip.NewRequest(sip.CANCEL, inviteRequest.Recipient)
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.Via())) // Cancel request must match invite TOP via and only have that Via
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.From()))
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.To()))
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.CallID()))
	sip.CopyHeaders("Route", inviteRequest, cancelReq)
	cancelReq.SetSource(inviteRequest.Source())
	cancelReq.SetDestination(inviteRequest.Destination())
	return cancelReq
}

type DialogClientCache struct {
	c       *Client
	dialogs sync.Map // TODO replace with typed version
	ua      DialogUA
}

// NewDialogClientCache provides simple cache layer for managing UAC dialogs.
// It is generally recomended to build your own cache layer
// Contact hdr is default to be provided for correct invite. It is not used if you provided hdr as part of request,
// but contact hdr must be present so this makes sure correct dialog is established.
// In case handling different transports you should have multiple instances per transport
func NewDialogClientCache(client *Client, contactHDR sip.ContactHeader) *DialogClientCache {
	s := &DialogClientCache{
		c:       client,
		dialogs: sync.Map{},
		ua: DialogUA{
			Client:     client,
			ContactHDR: contactHDR,
		},
	}
	return s
}

func (s *DialogClientCache) dialogsLen() int {
	leftItems := 0
	s.dialogs.Range(func(key, value any) bool {
		leftItems++
		return true
	})
	return leftItems
}

func (s *DialogClientCache) loadDialog(id string) *DialogClientSession {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogClientSession)
	return t
}

func (s *DialogClientCache) MatchRequestDialog(req *sip.Request) (*DialogClientSession, error) {
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

// Invite sends INVITE request and creates early dialog session.
// This is actually not yet dialog (ID is empty)
// You need to call WaitAnswer after for establishing dialog
// For passing custom Invite request use WriteInvite
func (c *DialogClientCache) Invite(ctx context.Context, recipient sip.Uri, body []byte, headers ...sip.Header) (*DialogClientSession, error) {
	dt, err := c.ua.Invite(ctx, recipient, body, headers...)
	if err != nil {
		return nil, err
	}

	dt.onClose = func() {
		c.dialogs.Delete(dt.ID)
	}

	dt.OnState(func(s sip.DialogState) {
		if s == sip.DialogStateEstablished {
			// Change of state is called after populating ID
			if dt.ID == "" {
				panic("id of dialog is empty")
			}
			c.dialogs.Store(dt.ID, dt)
		}
	})
	return dt, err
}

func (c *DialogClientCache) WriteInvite(ctx context.Context, inviteRequest *sip.Request) (*DialogClientSession, error) {
	dt, err := c.ua.WriteInvite(ctx, inviteRequest)
	if err != nil {
		return nil, err
	}

	dt.onClose = func() {
		c.dialogs.Delete(dt.ID)
	}

	dt.OnState(func(s sip.DialogState) {
		if s == sip.DialogStateEstablished {
			// Change of state is called after populating ID
			if dt.ID == "" {
				panic("id of dialog is empty")
			}
			c.dialogs.Store(dt.ID, dt)
		}
	})
	return dt, err
}

func (c *DialogClientCache) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	dt, err := c.MatchRequestDialog(req)
	if err != nil {
		return err
	}
	return dt.ReadBye(req, tx)
}
