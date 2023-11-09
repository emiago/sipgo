package sip

const (
	// Dialog received 200 response
	DialogStateEstablished = iota
	// Dialog received ACK
	DialogStateConfirmed
	// Dialog received BYE
	DialogStateEnded
)
