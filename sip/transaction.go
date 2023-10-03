package sip

type Transaction interface {
	// Terminate will terminate transaction
	Terminate()
	// Done when transaction fsm terminates. Can be called multiple times
	Done() <-chan struct{}
	// Last error. Useful to check when transaction terminates
	Err() error
}

type ServerTransaction interface {
	Transaction
	Respond(res *Response) error
	Acks() <-chan *Request
	Cancels() <-chan *Request
}

type ClientTransaction interface {
	Transaction
	// Responses returns channel with all responses for transaction
	Responses() <-chan *Response
	// Cancel sends cancel request
	Cancel() error
}
