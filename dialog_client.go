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
	UA       *DialogUA

	// onClose triggers when user calls Close
	onClose func()
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

// TransactionRequest is doing subsequent client DIALOG request based on RFC after initial session (INVITE) setup
// https://www.rfc-editor.org/rfc/rfc3261#section-12.2.1
// This ensures that you have proper request done within dialog. You should avoid setting any Dialog header (cseq, from, to, callid)
func (s *DialogClientSession) TransactionRequest(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	s.buildReq(req)

	// Passing option to avoid CSEQ apply
	return s.UA.Client.TransactionRequest(ctx, req, s.requestValidate)
}

func (s *DialogClientSession) WriteRequest(req *sip.Request) error {
	s.buildReq(req)
	return s.UA.Client.WriteRequest(req, s.requestValidate)
}

func (s *DialogClientSession) requestValidate(c *Client, req *sip.Request) error {
	if req.Via() == nil {
		ClientRequestAddVia(c, req)
	}
	// Makes sure Content-Length is present
	if req.Body() == nil {
		req.SetBody(nil)
	}
	return nil
}

func (s *DialogClientSession) buildReq(req *sip.Request) {
	// Keep any request inside dialog
	// var mustHaveHeaders []sip.Header = nil
	mustHaveHeaders := make([]sip.Header, 0, 5)
	if h, invH := req.From(), s.InviteRequest.From(); h == nil {
		mustHaveHeaders = append(mustHaveHeaders, sip.HeaderClone(invH))
	}

	if h, invH := req.To(), s.InviteResponse.To(); h == nil && invH != nil {
		mustHaveHeaders = append(mustHaveHeaders, sip.HeaderClone(invH))
	}

	if h, invH := req.CallID(), s.InviteRequest.CallID(); h == nil {
		mustHaveHeaders = append(mustHaveHeaders, sip.HeaderClone(invH))
	}

	if h := req.MaxForwards(); h == nil {
		maxFwd := sip.MaxForwardsHeader(70)
		mustHaveHeaders = append(mustHaveHeaders, &maxFwd)
	}

	cseq := req.CSeq()
	if cseq == nil {
		cseq = &sip.CSeqHeader{
			SeqNo:      s.InviteRequest.CSeq().SeqNo,
			MethodName: req.Method,
		}
		mustHaveHeaders = append(mustHaveHeaders, cseq)
	}
	if len(mustHaveHeaders) > 0 {
		req.PrependHeader(mustHaveHeaders...)
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
		// More on
		// https://datatracker.ietf.org/doc/html/rfc3261#section-16.12.1.1
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
		} else if s.UA.RewriteContact {
			req.SetDestination(s.InviteResponse.Source())
		}
	}

	if h := req.Contact(); h == nil {
		req.AppendHeader(sip.HeaderClone(&s.UA.ContactHDR))
	}

	s.lastCSeqNo.Store(cseq.SeqNo)
	// Make sure transport matches original invite
	req.SetTransport(s.InviteRequest.Transport())
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

// Invite creates transaction and sends invite.
func (d *DialogClientSession) Invite(ctx context.Context, options ...ClientRequestOption) error {
	cli := d.UA.Client
	inviteReq := d.InviteRequest

	var err error
	d.inviteTx, err = func() (sip.ClientTransaction, error) {
		// Try overriding contact header with via or local connection host port
		if cont := inviteReq.Contact(); cont.Address.Port == 0 {
			return cli.newTransaction(ctx, inviteReq, func(conn sip.Connection) error {
				// Using via instead connection to avoid double parsing
				// host, port, err = sip.ParseAddr(conn.LocalAddr().String())
				via := inviteReq.Via()
				if cont.Address.Host == "" {
					cont.Address.Host = via.Host
					cont.Address.Port = via.Port
					return nil
				}

				// In case host is FQDN we will not override
				if via.Host == cont.Address.Host {
					cont.Address.Port = via.Port
				}
				return nil
			}, options...)
		}

		return cli.TransactionRequest(ctx, inviteReq, options...)
	}()

	if err == nil {
		d.lastCSeqNo.Store(inviteReq.CSeq().SeqNo)
	}

	return err
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
	for i := 0; ; i++ {
		if i > 10 {
			// Preventing some long loops
			return fmt.Errorf("more than 10 responses received")
		}
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
			return s.inviteCancel(ctx, tx)
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
			h := inviteRequest.GetHeader("Proxy-Authorization")
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
				s.inviteTx = tx // We need to update this here as we can exit early like on provisional
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
				s.inviteTx = tx // We need to update this here as we can exit early like on provisional
				continue
			}
		}

		return &ErrDialogResponse{Res: r}
	}

	id, err := sip.DialogIDFromResponse(r)
	if err != nil {
		return err
	}
	s.inviteTx = tx
	s.InviteResponse = r
	s.ID = id
	s.setState(sip.DialogStateEstablished)
	return nil
}

func (s *DialogClientSession) inviteCancel(ctx context.Context, tx sip.ClientTransaction) error {
	if err := context.Cause(ctx); err == WaitAnswerForceCancelErr {
		// In case caller wants to force cancelation exit.
		return ctx.Err()
	}

	var r *sip.Response
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
}

// Ack sends ack. Use WriteAck for more customizing
func (s *DialogClientSession) Ack(ctx context.Context) error {
	ack := newAckRequestUAC(s.InviteRequest, s.InviteResponse, nil)
	return s.WriteAck(ctx, ack)
}

func (s *DialogClientSession) WriteAck(ctx context.Context, ack *sip.Request) error {
	// https://datatracker.ietf.org/doc/html/rfc3261#section-13.2.2.4
	//
	// Once the ACK has been constructed, the procedures of [4] are used to
	// determine the destination address, port and transport.  However, the
	// request is passed to the transport layer directly for transmission,
	// rather than a client transaction.  This is because the UAC core
	// handles retransmissions of the ACK, not the transaction layer.
	retransmissionAck := ack.Clone() // We need to clone for RACE safety
	s.inviteTx.OnRetransmission(func(r *sip.Response) {
		// Detect retransmission
		if r.StatusCode != 200 {
			return
		}

		if err := s.WriteRequest(retransmissionAck); err != nil {
			s.endWithCause(fmt.Errorf("ACK retransmission failed: %w", err))
		}
	})

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
	if s.InviteResponse == nil {
		return fmt.Errorf("bye: can not send as no invite response present")
	}
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

	// 	The caller's UA MAY send a BYE for either
	//    confirmed or early dialog
	if sip.DialogState(state) != sip.DialogStateConfirmed && !s.isEarlyDialog() {
		return fmt.Errorf("Dialog not confirmed or did not receive yet any response")
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

func (s *DialogClientSession) isEarlyDialog() bool {
	return s.InviteResponse != nil && s.InviteResponse.IsProvisional() && s.InviteResponse.StatusCode != 100
}

// newAckRequestUAC creates ACK request for 2xx INVITE
// https://tools.ietf.org/html/rfc3261#section-13.2.2.4
// NOTE: it does not copy Via header. This is left to transport or caller to enforce
func newAckRequestUAC(inviteRequest *sip.Request, inviteResponse *sip.Response, body []byte) *sip.Request {
	Recipient := &inviteRequest.Recipient
	if contact := inviteResponse.Contact(); contact != nil {
		Recipient = &contact.Address
	}
	ackRequest := sip.NewRequest(
		sip.ACK,
		*Recipient.Clone(),
	)
	ackRequest.SipVersion = inviteRequest.SipVersion

	if len(inviteRequest.GetHeaders("Route")) > 0 {
		sip.CopyHeaders("Route", inviteRequest, ackRequest)
	}

	if h := inviteRequest.From(); h != nil {
		ackRequest.AppendHeader(sip.HeaderClone(h))
	}

	if h := inviteResponse.To(); h != nil {
		ackRequest.AppendHeader(sip.HeaderClone(h))
	}

	if h := inviteRequest.CallID(); h != nil {
		ackRequest.AppendHeader(sip.HeaderClone(h))
	}

	if h := inviteRequest.CSeq(); h != nil {
		ackRequest.AppendHeader(sip.HeaderClone(h))
	}

	cseq := ackRequest.CSeq()
	cseq.MethodName = sip.ACK

	maxForwardsHeader := sip.MaxForwardsHeader(70)
	ackRequest.AppendHeader(&maxForwardsHeader)
	/*
	   	A UAC SHOULD include a Contact header field in any target refresh
	    requests within a dialog, and unless there is a need to change it,
	    the URI SHOULD be the same as used in previous requests within the
	    dialog.  If the "secure" flag is true, that URI MUST be a SIPS URI.
	    As discussed in Section 12.2.2, a Contact header field in a target
	    refresh request updates the remote target URI.  This allows a UA to
	    provide a new contact address, should its address change during the
	    duration of the dialog.
	*/

	if h := inviteRequest.Contact(); h != nil {
		ackRequest.AppendHeader(sip.HeaderClone(h))
	}

	ackRequest.SetBody(body)
	ackRequest.SetTransport(inviteRequest.Transport())
	ackRequest.SetSource(inviteRequest.Source())
	ackRequest.Laddr = inviteRequest.Laddr
	return ackRequest
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
	cancelReq.Laddr = inviteRequest.Laddr
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
	id, err := sip.DialogIDFromRequestUAC(req)
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
