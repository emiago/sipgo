package sip

const (
	// Dialog received 200 response
	DialogStateEstablished = iota
	// Dialog received ACK
	DialogStateConfirmed
	// Dialog received BYE
	DialogStateEnded
)

// DialogStateString maps state to string
func DialogStateString(state int) string {
	switch state {
	case DialogStateEstablished:
		return "established"

	case DialogStateConfirmed:
		return "confirmed"

	case DialogStateEnded:
		return "ended"
	default:
		return "unknown"
	}
}

// Dialog is data structure represanting dialog
type Dialog struct {
	// ID created by FROM tag, TO tag and Callid
	ID string
	// State of dialog. Check more for DialogState... constants
	State int
}

// StateString returns string version of state
// established, confirmed, ended
func (d *Dialog) StateString() string {
	return DialogStateString(d.State)
}
