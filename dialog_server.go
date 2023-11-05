package sipgo

import (
	"context"
	"fmt"
	"sync"

	"github.com/emiago/sipgo/sip"
)

type Dialog struct {
	ID string

	Invite   *sip.Request
	Response *sip.Response

	State int
}

func (d *Dialog) Body() []byte {
	return d.Response.Body()
}

type DialogServer struct {
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
	c          *Client
}

func (s *DialogServer) LoadDialog(id string) *DialogServerContext {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogServerContext)
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
// You need to use DialogServerContext for all further responses
func (s *DialogServer) ReadInvite(req *sip.Request, tx sip.ServerTransaction) *DialogServerContext {
	d := Dialog{
		Invite: req,
	}

	dtx := &DialogServerContext{
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
		return nil
	}

	dt := s.LoadDialog(id)
	if dt == nil {
		return fmt.Errorf("no existing dialog")
	}

	dt.State = sip.DialogStateConfirmed

	// Acks are normally just absorbed, but in case of proxy
	// they still need to be passed
	return nil
}

func (s *DialogServer) ReadBye(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		// Non dialog Bye?
		return nil
	}

	dt := s.LoadDialog(id)
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

type DialogServerContext struct {
	Dialog
	inviteTx sip.ServerTransaction
	s        *DialogServer
}

// Respond should be called for Invite request, you may want to call this multiple times like
// 100 Progress or 180 Ringing
// 2xx for creating dialog or other code in case failure
func (t *DialogServerContext) WriteResponse(statusCode sip.StatusCode, reason string, body []byte, headers ...sip.Header) error {
	tx := t.inviteTx
	// Must copy Record-Route headers. Done by this command
	res := sip.NewResponseFromRequest(t.Invite, statusCode, reason, body)
	// Must add contact header
	res.AppendHeader(&t.s.contactHDR)

	for _, h := range headers {
		res.AppendHeader(h)
	}

	if res.StatusCode/200 != 1 {
		// This will not create dialog so we will just respond
		return tx.Respond(res)
	}

	id, err := sip.MakeDialogIDFromResponse(res)
	if err != nil {
		return err
	}

	t.Dialog.ID = id
	t.Dialog.Response = res
	t.Dialog.State = sip.DialogStateEstablished

	t.s.dialogs.Store(id, t)
	return tx.Respond(res)
}

func (c *DialogServerContext) WriteBye(ctx context.Context) error {
	// This is tricky
	defer c.s.dialogs.Delete(c.ID) // Delete our dialog always
	defer c.inviteTx.Terminate()   // Terminates INVITE in all cases

	// Reverse from and to
	res := c.Dialog.Response

	cont, _ := res.Contact()
	bye := sip.NewRequest(sip.BYE, &cont.Address)

	from, _ := res.From()
	to, _ := res.To()

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

	tx, err := c.s.c.TransactionRequest(ctx, bye)
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
		return nil
	case <-tx.Done():
		return tx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
