package transaction

import (
	"fmt"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"

	"github.com/rs/zerolog"
)

type FSMfunc func() FSMfunc

type ServerTx struct {
	commonTx
	lastAck      *sip.Request
	lastCancel   *sip.Request
	acks         chan *sip.Request
	cancels      chan *sip.Request
	timer_g      *time.Timer
	timer_g_time time.Duration
	timer_h      *time.Timer
	timer_i      *time.Timer
	timer_i_time time.Duration
	timer_j      *time.Timer
	timer_1xx    *time.Timer
	timer_l      *time.Timer
	reliable     bool

	mu sync.RWMutex

	closeOnce sync.Once
}

func NewServerTx(key string, origin *sip.Request, conn transport.Connection, logger zerolog.Logger) *ServerTx {
	tx := new(ServerTx)
	tx.key = key
	tx.conn = conn

	// about ~10 retransmits
	tx.acks = make(chan *sip.Request)
	tx.cancels = make(chan *sip.Request)
	tx.errs = make(chan error)
	tx.done = make(chan struct{})
	tx.log = logger
	tx.origin = origin
	tx.reliable = transport.IsReliable(origin.Transport())
	return tx
}

func (tx *ServerTx) Init() error {
	tx.initFSM()

	tx.mu.Lock()

	if tx.reliable {
		tx.timer_i_time = 0
	} else {
		tx.timer_g_time = Timer_G
		tx.timer_i_time = Timer_I
	}

	tx.mu.Unlock()

	// RFC 3261 - 17.2.1
	if tx.Origin().IsInvite() {
		// tx.Log().Tracef("set timer_1xx to %v", Timer_1xx)
		tx.mu.Lock()
		tx.timer_1xx = time.AfterFunc(Timer_1xx, func() {
			trying := sip.NewResponseFromRequest(
				tx.Origin(),
				100,
				"Trying",
				nil,
			)
			// tx.Log().Trace("timer_1xx fired")
			if err := tx.Respond(trying); err != nil {
				tx.log.Error().Err(err).Msg("send '100 Trying' response failed")
			}
		})
		tx.mu.Unlock()
	}

	return nil
}

// Receive is endpoint for handling received server requests.
func (tx *ServerTx) Receive(req *sip.Request) error {
	input, err := tx.receiveRequest(req)
	if err != nil {
		return err
	}
	tx.spinFsm(input)
	return nil
}

func (tx *ServerTx) receiveRequest(req *sip.Request) (FsmInput, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.timer_1xx != nil {
		tx.timer_1xx.Stop()
		tx.timer_1xx = nil
	}

	switch {
	case req.Method == tx.origin.Method:
		return server_input_request, nil
	case req.IsAck(): // ACK for non-2xx response
		tx.lastAck = req
		return server_input_ack, nil
	case req.IsCancel():
		tx.lastCancel = req
		return server_input_cancel, nil
	}
	return FsmInputNone, fmt.Errorf("unexpected message error")
}

func (tx *ServerTx) Respond(res *sip.Response) error {
	if res.IsCancel() {
		return tx.conn.WriteMsg(res)
	}

	input, err := tx.receiveRespond(res)
	if err != nil {
		return err
	}
	tx.spinFsm(input)
	return nil
}

func (tx *ServerTx) receiveRespond(res *sip.Response) (FsmInput, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	tx.lastResp = res
	if tx.timer_1xx != nil {
		tx.timer_1xx.Stop()
		tx.timer_1xx = nil
	}

	switch {
	case res.IsProvisional():
		return server_input_user_1xx, nil
	case res.IsSuccess():
		return server_input_user_2xx, nil
	}
	return server_input_user_300_plus, nil
}

// Acks makes channel for sending acks. Channel is created on demand
func (tx *ServerTx) Acks() <-chan *sip.Request {
	return tx.acks
}

func (tx *ServerTx) passAck() {
	tx.mu.RLock()
	r := tx.lastAck
	tx.mu.RUnlock()

	if r == nil {
		return
	}
	// Go routines should be cheap and it will prevent blocking
	go tx.ackSend(r)
}

func (tx *ServerTx) ackSend(r *sip.Request) {
	select {
	case <-tx.done:
	case tx.acks <- r:
	}
}

func (tx *ServerTx) Cancels() <-chan *sip.Request {
	if tx.cancels != nil {
		return tx.cancels
	}
	tx.cancels = make(chan *sip.Request)
	return tx.cancels
}

func (tx *ServerTx) passCancel() {
	tx.mu.RLock()
	r := tx.lastCancel
	tx.mu.RUnlock()

	if r == nil {
		return
	}
	// Go routines should be cheap
	go tx.cancelSend(r)
}

func (tx *ServerTx) cancelSend(r *sip.Request) {
	select {
	case <-tx.done:
	case tx.cancels <- r:
	}
}

func (tx *ServerTx) passResp() error {
	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	if lastResp == nil {
		return fmt.Errorf("none response")
	}

	// tx.Log().Debug("actFinal")
	err := tx.conn.WriteMsg(lastResp)
	if err != nil {
		tx.log.Debug().Err(err).Str("res", lastResp.StartLine()).Msg("fail to pass response")
		tx.mu.Lock()
		tx.lastErr = err
		tx.mu.Unlock()
		return err
	}
	return nil
}

func (tx *ServerTx) sendErr(err error) {
	select {
	case <-tx.done:
	case tx.errs <- err:
	}
}

func (tx *ServerTx) Terminate() {
	tx.delete()
}

// func (tx *ServerTx) OnTerminate(f func()) {
// 	// NOT YET EXPOSED
// }

// Choose the right FSM init function depending on request method.
func (tx *ServerTx) initFSM() {
	tx.fsmMu.Lock()
	if tx.Origin().IsInvite() {
		tx.fsmState = tx.inviteStateProcceeding
	} else {
		tx.fsmState = tx.stateTrying
	}
	tx.fsmMu.Unlock()
}

func (tx *ServerTx) delete() {
	tx.closeOnce.Do(func() {
		tx.mu.Lock()
		close(tx.done)
		tx.mu.Unlock()
		tx.onTerminate(tx.key)
	})

	// time.Sleep(time.Microsecond)

	tx.mu.Lock()
	if tx.timer_i != nil {
		tx.timer_i.Stop()
		tx.timer_i = nil
	}
	if tx.timer_g != nil {
		tx.timer_g.Stop()
		tx.timer_g = nil
	}
	// tx.Log().Debug("transaction done")
	if tx.timer_h != nil {
		tx.timer_h.Stop()
		tx.timer_h = nil
	}
	if tx.timer_j != nil {
		tx.timer_j.Stop()
		tx.timer_j = nil
	}
	if tx.timer_1xx != nil {
		tx.timer_1xx.Stop()
		tx.timer_1xx = nil
	}
	tx.mu.Unlock()
	tx.log.Debug().Str("tx", tx.Key()).Msg("Destroyed")
}
