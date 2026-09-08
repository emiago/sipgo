package siptest

import (
	"log/slog"

	"github.com/emiago/sipgo/sip"
)

// ServerTxRecorder wraps server transactions
type ServerTxRecorder struct {
	*sip.ServerTx
	c *connRecorder
}

func NewServerTxRecorder(req *sip.Request) *ServerTxRecorder {
	key, err := sip.ServerTxKeyMake(req)
	if err != nil {
		panic(err)
	}
	conn := newConnRecorder()
	stx := sip.NewServerTx(key, req, conn, slog.Default())
	if err := stx.Init(); err != nil {
		panic(err)
	}
	return &ServerTxRecorder{
		stx,
		conn,
	}
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
