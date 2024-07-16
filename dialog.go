package sipgo

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/shpendbeqiraj2/sipgo/sip"
)

var (
	ErrDialogOutsideDialog   = errors.New("Call/Transaction outside dialog")
	ErrDialogDoesNotExists   = errors.New("Dialog Does Not Exist")
	ErrDialogInviteNoContact = errors.New("No Contact header")
	ErrDialogCanceled        = errors.New("Dialog canceled")
	ErrDialogInvalidCseq     = errors.New("Invalid CSEQ number")
)

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

func (d *Dialog) Body() []byte {
	return d.InviteResponse.Body()
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

func (d *Dialog) State() <-chan sip.DialogState {
	return d.stateCh
}

// Done is signaled when dialog state ended
//
// Deprecated:
// It is wrapper on context, so better to use Context()
func (d *Dialog) Done() <-chan struct{} {
	return d.ctx.Done()
}

func (d *Dialog) Context() context.Context {
	return d.ctx
}
