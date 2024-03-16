package sipgo

import (
	"errors"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
)

var (
	ErrDialogOutsideDialog   = errors.New("Call/Transaction outside dialog")
	ErrDialogDoesNotExists   = errors.New("Call/Transaction Does Not Exist")
	ErrDialogInviteNoContact = errors.New("No Contact header")
	ErrDialogCanceled        = errors.New("Dialog canceled")
)

type Dialog struct {
	ID string

	// InviteRequest is set when dialog is created. It is not thread safe!
	InviteRequest *sip.Request

	// InviteResponse is last response received or sent. It is not thread safe!
	InviteResponse *sip.Response

	state   atomic.Int32
	stateCh chan sip.DialogState

	done chan struct{}
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
		close(d.done) // Broadcasting done
	}
}

func (d *Dialog) State() <-chan sip.DialogState {
	return d.stateCh
}

func (d *Dialog) Done() <-chan struct{} {
	return d.done
}
