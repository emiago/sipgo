package siptest

import (
	"context"

	"github.com/emiago/sipgo/sip"
	"github.com/rs/zerolog/log"
)

type ClientTxRequester struct {
	OnRequest func(req *sip.Request) *sip.Response
}

func (r *ClientTxRequester) Request(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	key, _ := sip.MakeClientTxKey(req)
	rec := newConnRecorder()
	tx := sip.NewClientTx(key, req, rec, log.Logger)
	if err := tx.Init(); err != nil {
		return nil, err
	}

	resp := r.OnRequest(req)
	go tx.Receive(resp)

	return tx, nil
}
