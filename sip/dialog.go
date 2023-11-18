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
