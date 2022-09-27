package sip

const (
	// Dialog received 200 response
	DialogStateEstablished = iota
	// Dialog received ACK
	DialogStateConfirmed
	// Dialog received BYE
	DialogStateEnded
)

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

type Dialog struct {
	ID    string
	State int
}

func (d *Dialog) StateString() string {
	return DialogStateString(d.State)
}
