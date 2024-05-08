package sip

type DialogState int

const (
	// Dialog received 200 response
	DialogStateEstablished DialogState = 1
	// Dialog received ACK
	DialogStateConfirmed DialogState = 2
	// Dialog received BYE
	DialogStateEnded DialogState = 3
)

func (s DialogState) String() string {
	switch s {
	case DialogStateEstablished:
		return "Established"
	case DialogStateConfirmed:
		return "Confirmed"
	case DialogStateEnded:
		return "Ended"
	default:
		return "InProgress"
	}
}
