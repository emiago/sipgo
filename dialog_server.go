package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transaction"
)

type Dialog struct {
	ID string

	Invite   *sip.Request
	Response *sip.Response

	state int
}

var (
	ErrDialogOutsideDialog = errors.New("Call/Transaction outside dialog")
	ErrDialogDoesNotExists = errors.New("Call/Transaction Does Not Exist")
)

func (d *Dialog) Body() []byte {
	return d.Response.Body()
}

type DialogServer struct {
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
	c          *Client

	// OnSession is called just after sending Success response
	// It will block server side execution
	OnSession func(s *DialogServerSession)
}

func (s *DialogServer) LoadDialog(id string) *DialogServerSession {
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
func (s *DialogServer) ReadInvite(req *sip.Request, tx sip.ServerTransaction) *DialogServerSession {
	d := Dialog{
		Invite: req,
	}

	dtx := &DialogServerSession{
		Dialog:   d,
		inviteTx: tx,
		s:        s,
	}

	return dtx
}

// ReadAck should read from your OnAck handler
func (s *DialogServer) ReadAck(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		return errors.Join(ErrDialogOutsideDialog, err)
	}

	dt := s.LoadDialog(id)
	if dt == nil {
		// res := sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil)
		// if err := tx.Respond(res); err != nil {
		// 	return err
		// }
		return ErrDialogDoesNotExists
	}

	dt.mu.Lock()
	dt.state = sip.DialogStateConfirmed
	dt.mu.Unlock()

	// Acks are normally just absorbed, but in case of proxy
	// they still need to be passed
	return nil
}

func (s *DialogServer) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		return err
	}

	dt := s.LoadDialog(id)
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

type DialogServerSession struct {
	Dialog
	inviteTx sip.ServerTransaction
	s        *DialogServer
	mu       sync.Mutex
}

// Respond should be called for Invite request, you may want to call this multiple times like
// 100 Progress or 180 Ringing
// 2xx for creating dialog or other code in case failure
func (t *DialogServerSession) WriteResponse(statusCode sip.StatusCode, reason string, body []byte, headers ...sip.Header) error {
	tx := t.inviteTx
	// Must copy Record-Route headers. Done by this command
	res := sip.NewResponseFromRequest(t.Invite, statusCode, reason, body)
	// Must add contact header
	res.AppendHeader(&t.s.contactHDR)

	for _, h := range headers {
		res.AppendHeader(h)
	}

	if !res.IsSuccess() {
		// This will not create dialog so we will just respond
		return tx.Respond(res)
	}

	id, err := sip.MakeDialogIDFromResponse(res)
	if err != nil {
		return err
	}

	t.Dialog.ID = id
	t.Dialog.Response = res
	t.Dialog.state = sip.DialogStateEstablished

	t.s.dialogs.Store(id, t)

	if err := tx.Respond(res); err != nil {
		return err
	}

	// TODO: Should we maybe fork this
	if t.s.OnSession != nil {
		t.s.OnSession(t)
	}
	return nil
}

func (s *DialogServerSession) Bye(ctx context.Context) error {
	cli := s.s.c
	// This is tricky
	defer s.s.dialogs.Delete(s.ID) // Delete our dialog always
	defer s.inviteTx.Terminate()   // Terminates INVITE in all cases

	// Reverse from and to
	req := s.Dialog.Invite
	res := s.Dialog.Response

	if !res.IsSuccess() {
		return fmt.Errorf("Can not send bye on NON success response")
	}

	// https://datatracker.ietf.org/doc/html/rfc3261#section-15
	// However, the callee's UA MUST NOT send a BYE on a confirmed dialog
	// until it has received an ACK for its 2xx response or until the server
	// transaction times out.
	for {
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()

		if state < sip.DialogStateConfirmed {
			select {
			case <-s.inviteTx.Done():
				// Wait until we timeout
			case <-time.After(transaction.T1):
				// Recheck state
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		break
	}

	cont, _ := req.Contact()
	bye := sip.NewRequest(sip.BYE, &cont.Address)

	// Reverse from and to
	from, _ := res.From()
	to, _ := res.To()
	callid, _ := res.CallID()

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

	callidHDR, _ := bye.CallID()
	byeID := sip.MakeDialogID(callidHDR.Value(), newFrom.Params["tag"], newTo.Params["tag"])
	if s.ID != byeID {
		return fmt.Errorf("Non matching ID %q %q", s.ID, byeID)
	}

	tx, err := cli.TransactionRequest(ctx, bye)
	if err != nil {
		return err
	}
	defer tx.Terminate() // Terminates current transaction

	s.mu.Lock()
	s.state = sip.DialogStateEnded
	s.mu.Unlock()

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
