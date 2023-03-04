package sip

type Transaction interface {
	// Terminate will terminate transaction
	Terminate()
	// Done when transaction fsm terminates. Can be called multiple times
	Done() <-chan struct{}
	// Any errors will be passed via this channel
	Errors() <-chan error
}

type ServerTransaction interface {
	Transaction
	Respond(res *Response) error
	Acks() <-chan *Request
	Cancels() <-chan *Request
}

type ClientTransaction interface {
	Transaction
	Responses() <-chan *Response
	Cancel() error
}
