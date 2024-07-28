package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
)

var (
	ErrDialogOutsideDialog   = errors.New("Call/Transaction outside dialog")
	ErrDialogDoesNotExists   = errors.New("Dialog Does Not Exist")
	ErrDialogInviteNoContact = errors.New("No Contact header")
	ErrDialogCanceled        = errors.New("Dialog canceled")
	ErrDialogInvalidCseq     = errors.New("Invalid CSEQ number")
)

type ErrDialogResponse struct {
	Res *sip.Response
}

func (e ErrDialogResponse) Error() string {
	return fmt.Sprintf("Invite failed with response: %s", e.Res.StartLine())
}

type Dialog struct {
	ID string

	// InviteRequest is set when dialog is created. It is not thread safe!
	// Use it only as read only and use methods to change headers
	InviteRequest *sip.Request

	// lastCSeqNo is set for every request within dialog except ACK CANCEL
	lastCSeqNo uint32

	// InviteResponse is last response received or sent. It is not thread safe!
	// Use it only as read only and do not change values
	InviteResponse *sip.Response

	state   atomic.Int32
	stateCh chan sip.DialogState

	ctx    context.Context
	cancel context.CancelFunc
}

func (d *Dialog) setState(s sip.DialogState) {
	old := d.state.Swap(int32(s))
	if old == int32(s) {
		// Safety
		return
	}

	select {
	case d.stateCh <- s:
	default:
	}

	if s == sip.DialogStateEnded {
		d.cancel()
	}
}

func (d *Dialog) LoadState() sip.DialogState {
	return sip.DialogState(d.state.Load())
}

// Deprecated:
// Use StateRead
//
// Will be removed in future releases
func (d *Dialog) State() <-chan sip.DialogState {
	return d.stateCh
}

func (d *Dialog) StateRead() <-chan sip.DialogState {
	return d.stateCh
}

// func (d *Dialog) OnState(f func(s sip.DialogState)) {
// 	d.onState = append(d.onState, f)
// }

func (d *Dialog) Context() context.Context {
	return d.ctx
}
