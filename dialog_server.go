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

type DialogServerSession struct {
	Dialog
	inviteTx sip.ServerTransaction
	// s        *DialogServer
	ua *DialogUA

	// onClose is temporarly fix to handle dialog Closing.
	// Normally you want to have cleanup after dialog terminating or caller calling Close()
	// In future this could be only subscribing to dialog state
	onClose func()
}

// ReadAck changes dialog state to confiremed
func (s *DialogServerSession) ReadAck(req *sip.Request, tx sip.ServerTransaction) error {
	// cseq must match to our last dialog cseq
	if req.CSeq().SeqNo != s.lastCSeqNo.Load() {
		return ErrDialogInvalidCseq
	}
	s.setState(sip.DialogStateConfirmed)
	return nil
}

func (s *DialogServerSession) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	// Make sure this is bye for this dialog
	if err := s.validateRequest(req); err != nil {
		return err
	}

	defer s.Close()
	defer s.inviteTx.Terminate() // Terminat`es Invite transaction

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}

	s.setState(sip.DialogStateEnded)
	return nil
}

// Do does request response pattern. For more control over transaction use TransactionRequest
func (s *DialogServerSession) Do(ctx context.Context, req *sip.Request) (*sip.Response, error) {
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
// This ensures that you have proper request done within dialog
func (s *DialogServerSession) TransactionRequest(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	s.buildReq(req)
	// Passing option to avoid CSEQ apply
	return s.ua.Client.TransactionRequest(ctx, req, func(c *Client, req *sip.Request) error {
		if req.Via() == nil {
			ClientRequestAddVia(c, req)
		}
		// Makes sure Content-Length is present
		if req.Body() == nil {
			req.SetBody(nil)
		}
		return nil
	})
}

func (s *DialogServerSession) WriteRequest(req *sip.Request) error {
	s.buildReq(req)
	return s.ua.Client.WriteRequest(req)
}

func (s *DialogServerSession) buildReq(req *sip.Request) {
	// Keep any request inside dialog
	mustHaveHeaders := make([]sip.Header, 0, 5)
	if h, invH := req.From(), s.InviteResponse; h == nil && invH != nil {
		hh := invH.To().AsFrom()
		mustHaveHeaders = append(mustHaveHeaders, &hh)
	}

	if h, invH := req.To(), s.InviteRequest.From(); h == nil {
		hh := invH.AsTo()
		mustHaveHeaders = append(mustHaveHeaders, &hh)
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

	// https://datatracker.ietf.org/doc/html/rfc3261#section-16.12.1.2
	rrs := s.InviteRequest.GetHeaders("Record-Route")
	for i := range rrs {
		recordRoute := rrs[i]
		req.AppendHeader(sip.NewHeader("Route", recordRoute.Value()))
	}

	// Check Route Header
	// Should be handled by transport layer but here we are making this explicit
	if rr := req.Route(); rr != nil {
		req.SetDestination(rr.Address.HostPort())
	}
	// TODO check correct behavior strict routing vs loose routing
	// recordRoute := req.RecordRoute()
	// if recordRoute != nil {
	// 	if recordRoute.Address.UriParams.Has("lr") {
	// 		bye.AppendHeader(&sip.RouteHeader{Address: recordRoute.Address})
	// 	} else {
	// 		/* TODO
	// 		   If the route set is not empty, and its first URI does not contain the
	// 		   lr parameter, the UAC MUST place the first URI from the route set
	// 		   into the Request-URI, stripping any parameters that are not allowed
	// 		   in a Request-URI.  The UAC MUST add a Route header field containing
	// 		   the remainder of the route set values in order, including all
	// 		   parameters.  The UAC MUST then place the remote target URI into the
	// 		   Route header field as the last value.
	// 		*/
	// 	}
	// }

	s.lastCSeqNo.Store(cseq.SeqNo)

	if h := req.Contact(); h == nil {
		req.AppendHeader(sip.HeaderClone(&s.ua.ContactHDR))
	}

	if s.ua.RewriteContact && len(rrs) == 0 {
		req.SetDestination(s.InviteRequest.Source())
	}

	// TODO check is contact header routable
	// If not then we should force destination as source address
	req.SetTransport(s.InviteRequest.Transport())
}

// Close is always good to call for cleanup or terminating dialog state
func (s *DialogServerSession) Close() error {
	if s.onClose != nil {
		s.onClose()
	}
	return nil
}

// Respond should be called for Invite request, you may want to call this multiple times like
// 100 Progress or 180 Ringing
// 2xx for creating dialog or other code in case failure
//
// In case Cancel request received: ErrDialogCanceled is responded
func (s *DialogServerSession) Respond(statusCode int, reason string, body []byte, headers ...sip.Header) error {
	// Must copy Record-Route headers. Done by this command
	res := sip.NewResponseFromRequest(s.InviteRequest, statusCode, reason, body)

	for _, h := range headers {
		res.AppendHeader(h)
	}

	return s.WriteResponse(res)
}

// RespondSDP is just wrapper to call 200 with SDP.
// It is better to use this when answering as it provide correct headers
func (s *DialogServerSession) RespondSDP(sdp []byte) error {
	if sdp == nil {
		return fmt.Errorf("sdp not provided")
	}
	res := sip.NewSDPResponseFromRequest(s.InviteRequest, sdp)
	return s.WriteResponse(res)
}

var errDialogUnauthorized = errors.New("unathorized")

func (s *DialogServerSession) authDigest(chal *digest.Challenge, opts digest.Options) error {
	authorized := func() bool {
		authorizationHDR := s.InviteRequest.GetHeader("Authorization")
		if authorizationHDR == nil {
			return false
		}

		hdrVal := authorizationHDR.Value()
		creds, err := digest.ParseCredentials(hdrVal)
		if err != nil {
			return false
		}

		digCred, err := digest.Digest(chal, opts)
		if err != nil {
			return false
		}

		return creds.Response == digCred.Response
	}()

	if authorized {
		return nil
	}

	hdrVal := chal.String()
	hdr := sip.NewHeader("WWW-Authenticate", hdrVal)

	res := sip.NewResponseFromRequest(s.InviteRequest, sip.StatusUnauthorized, "Unauthorized", nil)
	res.AppendHeader(hdr)
	if err := s.WriteResponse(res); err != nil {
		return err
	}

	return errDialogUnauthorized
}

// WriteResponse allows passing you custom response
func (s *DialogServerSession) WriteResponse(res *sip.Response) error {
	tx := s.inviteTx

	if res.Contact() == nil {
		// Add our default contact header
		res.AppendHeader(&s.ua.ContactHDR)
	}

	s.Dialog.InviteResponse = res

	// Do we have cancel in meantime
	select {
	case <-tx.Done():
		// There must be some error
		return tx.Err()
	default:
	}

	if !res.IsSuccess() {
		if res.IsProvisional() {
			// This will not create dialog so we will just respond
			return tx.Respond(res)
		}

		// For final response we want to set dialog ended state
		if err := tx.Respond(res); err != nil {
			return err
		}

		// We should wait ACK for cleaner exit
		select {
		case <-tx.Acks():
		case <-tx.Done():
			// This means tx moved to terminated state and no more invite retransmissions is accepted
		}
		s.setState(sip.DialogStateEnded)
		return nil
	}

	id, err := sip.MakeDialogIDFromResponse(res)
	if err != nil {
		return err
	}

	if id != s.Dialog.ID {
		return fmt.Errorf("ID do not match. Invite request has changed headers?")
	}

	s.setState(sip.DialogStateEstablished)
	if err := tx.Respond(res); err != nil {
		return err
	}

	// Wait now for ACK for our 2xx
	// https://datatracker.ietf.org/doc/html/rfc3261#section-13.3.1.4

	// We are following RFC 6026, which states that this is TU thing and not Transaction layer.
	timer := time.NewTimer(sip.T1)
	defer timer.Stop()

	state := sip.DialogStateEstablished
	readStateCh := s.StateRead()
	for state == sip.DialogStateEstablished {
		select {
		case <-timer.C:
			if err := tx.Respond(res); err != nil {
				return err
			}
			// 2xx response is passed to the transport with an
			//    interval that starts at T1 seconds and doubles for each
			//    retransmission until it reaches T2 seconds (T1 and T2 are defined in
			//    Section 17).
			timer.Reset(max(2*sip.T1, sip.T2))

		case <-time.After(64 * sip.T1):
			// If the server retransmits the 2xx response for 64*T1 seconds without
			// receiving an ACK, the dialog is confirmed, but the session SHOULD be
			// terminated.  This is accomplished with a BYE, as described in Section
			// 15.
			state = sip.DialogStateConfirmed
		case state = <-readStateCh:
		}
	}
	if state != sip.DialogStateConfirmed {
		return fmt.Errorf("No ACK received")
	}
	return nil
}

func (s *DialogServerSession) Bye(ctx context.Context) error {
	req := s.Dialog.InviteRequest
	cont := s.Dialog.InviteRequest.Contact()
	bye := sip.NewRequest(sip.BYE, cont.Address)
	bye.SetTransport(req.Transport())

	return s.WriteBye(ctx, bye)
}

func (s *DialogServerSession) WriteBye(ctx context.Context, bye *sip.Request) error {
	state := s.state.Load()
	// In case dialog terminated
	if sip.DialogState(state) == sip.DialogStateEnded {
		return nil
	}

	// https://datatracker.ietf.org/doc/html/rfc3261#section-15
	// However, the callee's UA MUST NOT send a BYE on a confirmed dialog
	// until it has received an ACK for its 2xx response or until the server
	// transaction times out.
	if sip.DialogState(state) != sip.DialogStateConfirmed {
		return nil
	}

	res := s.Dialog.InviteResponse

	if !res.IsSuccess() {
		return fmt.Errorf("can not send bye on NON success response")
	}

	// This is tricky
	defer s.inviteTx.Terminate() // Terminates INVITE in all cases
	for {
		state = s.state.Load()
		if sip.DialogState(state) < sip.DialogStateConfirmed {
			select {
			case <-s.inviteTx.Done():
				// Wait until we timeout
			case <-time.After(sip.T1):
				// Recheck state
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		break
	}

	tx, err := s.TransactionRequest(ctx, bye)
	if err != nil {
		return err
	}
	defer tx.Terminate() // Terminates current transaction

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

func (dt *DialogServerSession) validateRequest(req *sip.Request) (err error) {
	// Make sure this is bye for this dialog

	if req.CSeq().SeqNo < dt.InviteRequest.CSeq().SeqNo {
		return ErrDialogInvalidCseq
	}
	return nil
}

// DialogServerCache serves as quick way to start building dialog server
// It is not optimized version and it is recomended that you build own dialog caching
type DialogServerCache struct {
	dialogs sync.Map // TODO replace with typed version
	ua      DialogUA
}

func (s *DialogServerCache) loadDialog(id string) *DialogServerSession {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogServerSession)
	return t
}

func (s *DialogServerCache) MatchDialogRequest(req *sip.Request) (*DialogServerSession, error) {
	id, err := sip.UASReadRequestDialogID(req)
	if err != nil {
		return nil, errors.Join(ErrDialogOutsideDialog, err)
	}

	dt := s.loadDialog(id)
	if dt == nil {
		return nil, ErrDialogDoesNotExists
	}
	return dt, nil
}

// NewDialogServerCache provides simple cache layer for managing UAS dialog
// Contact hdr is default that is provided for responses.
// Client is needed for termination dialog session
// In case handling different transports you should have multiple instances per transport
//
// Using DialogUA is now better way for genereting dialogs without caching and giving you as caller whole control of dialog
func NewDialogServerCache(client *Client, contactHDR sip.ContactHeader) *DialogServerCache {
	s := &DialogServerCache{
		dialogs: sync.Map{},
		ua: DialogUA{
			Client:     client,
			ContactHDR: contactHDR,
		},
	}
	return s
}

// ReadInvite should read from your OnInvite handler for which it creates dialog context
// You need to use DialogServerSession for all further responses
// Do not forget to add ReadAck and ReadBye for confirming dialog and terminating
func (s *DialogServerCache) ReadInvite(req *sip.Request, tx sip.ServerTransaction) (*DialogServerSession, error) {
	dtx, err := s.ua.ReadInvite(req, tx)
	if err != nil {
		return nil, err
	}

	id := dtx.ID
	dtx.onClose = func() {
		s.dialogs.Delete(id)
	}
	s.dialogs.Store(id, dtx)
	return dtx, nil
}

// ReadAck should read from your OnAck handler
func (s *DialogServerCache) ReadAck(req *sip.Request, tx sip.ServerTransaction) error {
	dt, err := s.MatchDialogRequest(req)
	if err != nil {
		return err
	}
	return dt.ReadAck(req, tx)
}

// ReadBye should read from your OnBye handler. Returns error if it fails
func (s *DialogServerCache) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	dt, err := s.MatchDialogRequest(req)
	if err != nil {
		return err
	}
	return dt.ReadBye(req, tx)
}
