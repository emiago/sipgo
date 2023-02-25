package sipgo

import (
	"github.com/emiago/sipgo/sip"
)

// ServerDialog is extension of Server to support Dialog handling
type ServerDialog struct {
	Server

	onDialog func(d sip.Dialog)
}

func NewServerDialog(ua *UserAgent, options ...ServerOption) (*ServerDialog, error) {
	base, err := newBaseServer(ua, options...)
	if err != nil {
		return nil, err
	}

	s := &ServerDialog{
		Server: *base,
	}

	// s.tp = transport.NewLayer(s.dnsResolver)
	// Handle our transaction layer requests
	s.tx.OnRequest(s.onRequestDialog)
	return s, nil
}

func (s *ServerDialog) onRequestDialog(r *sip.Request, tx sip.ServerTransaction) {
	go s.handleRequestDialog(r, tx)
}

func (s *ServerDialog) handleRequestDialog(r *sip.Request, tx sip.ServerTransaction) {
	switch r.Method {
	// Early state
	// case sip.INVITE:

	case sip.ACK:
		s.publish(r, sip.Dialog{
			State: sip.DialogStateConfirmed,
		})

	case sip.BYE:
		s.publish(r, sip.Dialog{
			State: sip.DialogStateEnded,
		})
	}

	// This makes allocation, but hard to override
	// Maybe goign on transaction layer
	wraptx := &dialogServerTx{tx, s}
	s.Server.handleRequest(r, wraptx)
}

func (s *ServerDialog) publish(r sip.Message, d sip.Dialog) {
	id, err := sip.MakeDialogIDFromMessage(r)
	if err != nil {
		s.log.Error().Err(err).Str("msg", sip.MessageShortString(r)).Msg("Failed to create dialog id")
		return
	}

	d.ID = id
	s.onDialog(d)
}

// OnDialog allows monitoring new dialogs
func (s *ServerDialog) OnDialog(f func(d sip.Dialog)) {
	s.onDialog = f
}

// OnDialogChan same as onDialog but we channel instead callback func
func (s *ServerDialog) OnDialogChan(ch chan sip.Dialog) {
	s.onDialog = func(d sip.Dialog) {
		ch <- d
	}
}

// this is just wrapper to allow listening response
type dialogServerTx struct {
	sip.ServerTransaction
	s *ServerDialog
}

func (tx *dialogServerTx) Respond(r *sip.Response) error {
	switch {
	// EARLY STATE NEEDS more definition
	// case r.IsProvisional():

	case r.IsSuccess():
		tx.s.publish(r, sip.Dialog{
			State: sip.DialogStateEstablished,
		})
	}

	return tx.ServerTransaction.Respond(r)
}
