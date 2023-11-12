package sip

const (
	// Dialog received 200 response
	DialogStateEstablished int32 = 1
	// Dialog received ACK
	DialogStateConfirmed int32 = 2
	// Dialog received BYE
	DialogStateEnded int32 = 3
)
