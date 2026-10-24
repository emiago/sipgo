package siptest

import (
	"context"
	"log/slog"

	"github.com/emiago/sipgo/sip"
)

type ClientTxRequester struct {
	OnRequest func(req *sip.Request) *sip.Response
}

func (r *ClientTxRequester) Request(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	key, _ := sip.ClientTxKeyMake(req)
	rec := newConnRecorder()
	tx := sip.NewClientTx(key, req, rec, slog.Default())
	if err := tx.Init(); err != nil {
		return nil, err
	}

	resp := r.OnRequest(req)
	go tx.Receive(resp)

	return tx, nil
}

type ClientTxResponder struct {
	tx *sip.ClientTx
}

func (r *ClientTxResponder) Receive(res *sip.Response) {
	r.tx.Receive(res)
}

// Requests returns a snapshot of sent requests, including retransmissions.
func (r *ClientTxResponder) Requests() []*sip.Request {
	rec := r.tx.Connection().(*connRecorder)
	rec.mu.Lock()
	defer rec.mu.Unlock()

	requests := make([]*sip.Request, len(rec.msgs))
	for i, msg := range rec.msgs {
		requests[i] = msg.(*sip.Request).Clone()
	}
	return requests
}

type ClientTxRequesterResponder struct {
	OnRequest func(req *sip.Request, w *ClientTxResponder)
}

func (r *ClientTxRequesterResponder) Request(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	key, _ := sip.ClientTxKeyMake(req)
	rec := newConnRecorder()
	tx := sip.NewClientTx(key, req, rec, slog.Default())
	if err := tx.Init(); err != nil {
		return nil, err
	}
	w := ClientTxResponder{
		tx: tx,
	}
	go r.OnRequest(req, &w)
	return tx, nil
}
