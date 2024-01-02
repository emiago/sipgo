package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo/sip"
)

type DialogServer struct {
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
	c          *Client
}

func (s *DialogServer) loadDialog(id string) *DialogServerSession {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogServerSession)
	return t
}

// NewDialogServer provides handle for managing UAS dialog
// Contact hdr must be provided for responses
// Client is needed for termination dialog session
// In case handling different transports you should have multiple instances per transport
func NewDialogServer(client *Client, contactHDR sip.ContactHeader) *DialogServer {
	s := &DialogServer{
		dialogs:    sync.Map{},
		contactHDR: contactHDR,
		c:          client,
	}
	return s
}

// ReadInvite should read from your OnInvite handler for which it creates dialog context
// You need to use DialogServerSession for all further responses
func (s *DialogServer) ReadInvite(req *sip.Request, tx sip.ServerTransaction) (*DialogServerSession, error) {
	cont := req.Contact()
	if cont == nil {
		return nil, ErrDialogInviteNoContact
	}

	dtx := &DialogServerSession{
		Dialog: Dialog{
			InviteRequest: req,
			state:         atomic.Int32{},
			stateCh:       make(chan sip.DialogState, 3),
			done:          make(chan struct{}),
		},
		inviteTx: tx,
		s:        s,
	}

	return dtx, nil
}

// ReadAck should read from your OnAck handler
func (s *DialogServer) ReadAck(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		return errors.Join(ErrDialogOutsideDialog, err)
	}

	dt := s.loadDialog(id)
	if dt == nil {
		// res := sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil)
		// if err := tx.Respond(res); err != nil {
		// 	return err
		// }
		return ErrDialogDoesNotExists
	}

	dt.setState(sip.DialogStateConfirmed)

	// Acks are normally just absorbed, but in case of proxy
	// they still need to be passed
	return nil
}

func (s *DialogServer) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		return err
	}

	dt := s.loadDialog(id)
	if dt == nil {
		// https://datatracker.ietf.org/doc/html/rfc3261#section-15.1.2
		// If the BYE does not
		//    match an existing dialog, the UAS core SHOULD generate a 481
		//    (Call/Transaction Does Not Exist)
		// res := sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil)
		// if err := tx.Respond(res); err != nil {
		// 	return err
		// }
		return ErrDialogDoesNotExists
	}
	defer dt.Close()
	defer dt.inviteTx.Terminate() // Terminates Invite transaction

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}

	return nil
}

type DialogServerSession struct {
	Dialog
	inviteTx sip.ServerTransaction
	s        *DialogServer
}

// Close is always good to call for cleanup or terminating dialog state
func (s *DialogServerSession) Close() error {
	s.s.dialogs.Delete(s.ID)
	s.setState(sip.DialogStateEnded)
	// ctx, _ := context.WithTimeout(context.Background(), transaction.Timer_B)
	// return s.Bye(ctx)
	return nil
}

// Respond should be called for Invite request, you may want to call this multiple times like
// 100 Progress or 180 Ringing
// 2xx for creating dialog or other code in case failure
//
// In case Cancel request received: ErrDialogCanceled is responded
func (s *DialogServerSession) Respond(statusCode sip.StatusCode, reason string, body []byte, headers ...sip.Header) error {
	// Must copy Record-Route headers. Done by this command
	res := sip.NewResponseFromRequest(s.InviteRequest, statusCode, reason, body)

	for _, h := range headers {
		res.AppendHeader(h)
	}

	return s.WriteResponse(res)
}

func (s *DialogServerSession) WriteResponse(res *sip.Response) error {
	tx := s.inviteTx

	// Must add contact header
	res.AppendHeader(&s.s.contactHDR)

	// Do we have cancel in meantime
	select {
	case req := <-tx.Cancels():
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil))
		return ErrDialogCanceled
	case <-tx.Done():
		// There must be some error
		return tx.Err()
	default:
	}

	if !res.IsSuccess() {
		// This will not create dialog so we will just respond
		return tx.Respond(res)
	}

	id, err := sip.MakeDialogIDFromResponse(res)
	if err != nil {
		return err
	}

	s.Dialog.ID = id
	s.Dialog.InviteResponse = res
	s.setState(sip.DialogStateEstablished)

	if err := tx.Respond(res); err != nil {
		return err
	}

	s.s.dialogs.Store(id, s)
	return nil
}

func (s *DialogServerSession) Bye(ctx context.Context) error {
	state := s.state.Load()
	// In case dialog terminated
	if sip.DialogState(state) == sip.DialogStateEnded {
		return nil
	}

	cli := s.s.c
	req := s.Dialog.InviteRequest
	res := s.Dialog.InviteResponse

	if !res.IsSuccess() {
		return fmt.Errorf("Can not send bye on NON success response")
	}

	// This is tricky
	defer s.Close()              // Delete our dialog always
	defer s.inviteTx.Terminate() // Terminates INVITE in all cases

	// https://datatracker.ietf.org/doc/html/rfc3261#section-15
	// However, the callee's UA MUST NOT send a BYE on a confirmed dialog
	// until it has received an ACK for its 2xx response or until the server
	// transaction times out.
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

	cont := req.Contact()
	bye := sip.NewRequest(sip.BYE, &cont.Address)

	// Reverse from and to
	from := res.From()
	to := res.To()
	callid := res.CallID()

	newFrom := &sip.FromHeader{
		DisplayName: to.DisplayName,
		Address:     to.Address,
		Params:      to.Params,
	}

	newTo := &sip.ToHeader{
		DisplayName: from.DisplayName,
		Address:     from.Address,
		Params:      from.Params,
	}

	bye.AppendHeader(newFrom)
	bye.AppendHeader(newTo)
	bye.AppendHeader(callid)

	callidHDR := bye.CallID()
	byeID := sip.MakeDialogID(callidHDR.Value(), newFrom.Params["tag"], newTo.Params["tag"])
	if s.ID != byeID {
		return fmt.Errorf("Non matching ID %q %q", s.ID, byeID)
	}

	tx, err := cli.TransactionRequest(ctx, bye)
	if err != nil {
		return err
	}
	defer tx.Terminate() // Terminates current transaction

	// s.setState(sip.DialogStateEnded)

	// Wait 200
	select {
	case res := <-tx.Responses():
		if res.StatusCode != 200 {
			return ErrDialogResponse{res}
		}
		return nil
	case <-tx.Done():
		return tx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
