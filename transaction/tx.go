package transaction

import (
	"sync"

	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
)

type commonTx struct {
	key string

	origin *sip.Request
	// tpl    *transport.Layer

	conn     sip.Connection
	lastResp *sip.Response

	lastErr error
	done    chan struct{}

	//State machine control
	fsmMu    sync.RWMutex
	fsmState FsmContextState

	log         zerolog.Logger
	onTerminate FnTxTerminate
}

func (tx *commonTx) String() string {
	if tx == nil {
		return "<nil>"
	}

	// fields := tx.Log().Fields().WithFields(log.Fields{
	// 	"key": tx.key,
	// })
	return tx.key

	// return fmt.Sprintf("%s<%s>", tx.Log().Prefix(), fields)
}

func (tx *commonTx) Origin() *sip.Request {
	return tx.origin
}

func (tx *commonTx) Key() string {
	return tx.key
}

// func (tx *commonTx) Transport() sip.Transport {
// 	return tx.tpl
// }

func (tx *commonTx) Done() <-chan struct{} {
	return tx.done
}

func (tx *commonTx) OnTerminate(f FnTxTerminate) {
	tx.onTerminate = f
}

// Choose the right FSM init function depending on request method.
func (tx *commonTx) spinFsm(in FsmInput) {
	tx.fsmMu.Lock()
	for i := in; i != FsmInputNone; {
		i = tx.fsmState(i)
	}
	tx.fsmMu.Unlock()
}
