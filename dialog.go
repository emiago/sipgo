package sipgo

import (
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

type DialogServer struct {
	dialogs    sync.Map // TODO replace with typed version
	contactHDR sip.ContactHeader
}

func (s *DialogServer) LoadDialog(id string) *DialogServerTransaction {
	val, ok := s.dialogs.Load(id)
	if !ok || val == nil {
		return nil
	}

	t := val.(*DialogServerTransaction)
	return t
}

// NewDialogServer provides handle for managing UAS dialog
// Contact hdr must be provided for responses
func NewDialogServer(contactHDR sip.ContactHeader) *DialogServer {
	s := &DialogServer{
		dialogs:    sync.Map{},
		contactHDR: contactHDR,
	}
	return s
}

func (s *DialogServer) Invite(req *sip.Request, tx sip.ServerTransaction) *DialogServerTransaction {
	d := Dialog{
		Invite: req,
	}

	dtx := &DialogServerTransaction{
		Dialog:            d,
		ServerTransaction: tx,
		s:                 s,
	}

	return dtx
}

func (s *DialogServer) Ack(req *sip.Request, tx sip.ServerTransaction) error {
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

func (s *DialogServer) Bye(req *sip.Request, tx sip.ServerTransaction) error {
	id, err := sip.MakeDialogIDFromRequest(req)
	if err != nil {
		// Non dialog Bye?
		return nil
	}

	dt := s.LoadDialog(id)
	if dt == nil {
		return fmt.Errorf("no existing dialog")
	}
	defer dt.Terminate() // Terminates Invite transaction

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if err := tx.Respond(res); err != nil {
		return err
	}

	select {
	case <-tx.Done():
		return tx.Err()
	}
}

type DialogServerTransaction struct {
	sip.ServerTransaction
	Dialog
	s *DialogServer
}

// Respond should be called for Invite request, you may want to call this multiple times like
// 100 Progress or 180 Ringing
// 2xx for creating dialog or other code in case failure
func (t *DialogServerTransaction) WriteResponse(statusCode sip.StatusCode, reason string, body []byte, headers ...sip.Header) error {
	// Must copy Record-Route headers. Done by this command
	res := sip.NewResponseFromRequest(t.Invite, statusCode, reason, body)
	// Must add contact header
	res.AppendHeader(&t.s.contactHDR)

	for _, h := range headers {
		res.AppendHeader(h)
	}

	if res.StatusCode/200 != 1 {
		// This will not create dialog so we will just respond
		return t.Respond(res)
	}

	id, err := sip.MakeDialogIDFromResponse(res)
	if err != nil {
		return err
	}

	t.Dialog.ID = id
	t.Dialog.Response = res
	t.Dialog.State = sip.DialogStateEstablished

	t.s.dialogs.Store(id, t)
	return t.Respond(res)
}
