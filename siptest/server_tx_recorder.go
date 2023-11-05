package siptest

import (
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transaction"

	"github.com/rs/zerolog/log"
)

func NewServerTxRecorder(req *sip.Request) *ServerTxRecorder {
	// stx := transaction.NewServerTx()

	key, err := transaction.MakeServerTxKey(req)
	if err != nil {
		panic(err)
	}
	conn := newConnRecorder()
	stx := transaction.NewServerTx(key, req, conn, log.Logger)
	if err := stx.Init(); err != nil {
		panic(err)
	}
	return &ServerTxRecorder{
		stx,
		conn,
	}
}

// ServerTxRecorder wraps server transactions
type ServerTxRecorder struct {
	*transaction.ServerTx
	c *connRecorder
}

// Result returns sip response. Can be nil if none was processed
func (r *ServerTxRecorder) Result() []*sip.Response {
	if len(r.c.msgs) == 0 {
		return nil
	}
	resps := make([]*sip.Response, len(r.c.msgs))
	for i, m := range r.c.msgs {
		resps[i] = m.(*sip.Response).Clone()
	}

	return resps
}

// func (r *ServerTxRecorder) Terminate() {

// }

// func (r *ServerTxRecorder) Done() <-chan struct{} {

// }

// func (r *ServerTxRecorder) Err() error {

// }

// func (r *ServerTxRecorder) Respond(res *sip.Response) error {

// }

// func (r *ServerTxRecorder) Acks() <-chan *sip.Request {

// }

// func (r *ServerTxRecorder) Cancels() <-chan *sip.Request {

// }

// var _ sip.ServerTransaction = &ServerTxRecorder{}
