package sipgo

import "github.com/emiago/sipgo/sip"

type NoOpTransaction struct {
	respCh <-chan *sip.Response
	doneCh <-chan struct{}
}

var _ sip.Transaction = &NoOpTransaction{}

func (t *NoOpTransaction) Terminate() {}

func (t *NoOpTransaction) Done() <-chan struct{} {
	if t.doneCh != nil {
		return t.doneCh
	}
	doneCh := make(chan struct{})
	close(doneCh)
	return doneCh
}

func (t *NoOpTransaction) Err() error {
	return nil
}

// Responses implements sip.ClientTransaction interface.
func (t *NoOpTransaction) Responses() <-chan *sip.Response {
	if t.respCh != nil {
		return t.respCh
	}
	respCh := make(chan *sip.Response)
	close(respCh)
	return respCh
}

func (t *NoOpTransaction) OnTerminate(_ sip.FnTxTerminate) bool {
	return false
}

// setResponses sets the response channel for this transaction
func (t *NoOpTransaction) setResponses(ch <-chan *sip.Response) {
	t.respCh = ch
}

// setDone sets the done channel for this transaction
func (t *NoOpTransaction) setDone(ch <-chan struct{}) {
	t.doneCh = ch
}

type NoOpServerTransaction struct {
	NoOpTransaction
}

var _ sip.ServerTransaction = &NoOpServerTransaction{}

func (t *NoOpServerTransaction) Respond(_ *sip.Response) error {
	return nil
}

func (t *NoOpServerTransaction) Acks() <-chan *sip.Request {
	reqCh := make(chan *sip.Request)
	close(reqCh)
	return reqCh
}

func (t *NoOpTransaction) OnCancel(_ sip.FnTxCancel) bool {
	return false
}
