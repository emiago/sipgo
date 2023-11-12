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
)

type Dialog struct {
	ID string

	InviteRequest  *sip.Request
	InviteResponse *sip.Response

	state atomic.Int32
}

func (d *Dialog) Body() []byte {
	return d.InviteResponse.Body()
}

func (d *Dialog) setState(s int32) {
	d.state.Store(s)
}
