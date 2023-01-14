package sip

type Transaction interface {
	Terminate()
	Done() <-chan bool
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
