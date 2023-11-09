package sipgo

import (
	"errors"

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

	state int
}

func (d *Dialog) Body() []byte {
	return d.InviteResponse.Body()
}
