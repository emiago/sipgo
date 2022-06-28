package sip

type Transaction interface {
	Init() error
	Key() string
	Origin() *Request
	String() string
	// Transport() Transport
	Terminate()
	Done() <-chan bool
	Errors() <-chan error
}

type ServerTransaction interface {
	Transaction
	Receive(req *Request) error
	Respond(res *Response) error
	Acks() <-chan *Request
	Cancels() <-chan *Request
}

type ClientTransaction interface {
	Transaction
	Receive(res *Response) error
	Responses() <-chan *Response
	Cancel() error
}
