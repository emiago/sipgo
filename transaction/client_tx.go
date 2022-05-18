package transaction

import (
	"sync"
	"time"

	"github.com/emiraganov/sipgo/sip"
	"github.com/emiraganov/sipgo/transport"

	"github.com/rs/zerolog"

	"github.com/ghettovoice/gosip/timing"
)

type ClientTx struct {
	commonTx
	responses    chan *sip.Response
	timer_a_time time.Duration // Current duration of timer A.
	timer_a      timing.Timer
	timer_b      timing.Timer
	timer_d_time time.Duration // Current duration of timer D.
	timer_d      timing.Timer
	timer_m      timing.Timer
	reliable     bool

	mu        sync.RWMutex
	closeOnce sync.Once
}

func NewClientTx(key string, origin *sip.Request, tpl *transport.Layer, logger zerolog.Logger) *ClientTx {
	origin = prepareClientRequest(origin)
	tx := &ClientTx{}
	tx.key = key
	tx.tpl = tpl
	// buffer chan - about ~10 retransmit responses
	tx.responses = make(chan *sip.Response)
	tx.errs = make(chan error)
	tx.done = make(chan bool)
	tx.log = logger

	tx.origin = origin
	tx.reliable = transport.IsReliable(origin.Transport())
	return tx
}

func prepareClientRequest(origin *sip.Request) *sip.Request {
	if viaHop, ok := origin.Via(); ok {
		if viaHop.Params == nil {
			viaHop.Params = sip.NewParams()
		}
		if !viaHop.Params.Has("branch") {
			viaHop.Params.Add("branch", sip.GenerateBranch())
		}
	} else {
		viaHop = &sip.ViaHeader{
			ProtocolName:    "SIP",
			ProtocolVersion: "2.0",
			Params: sip.NewParams().
				Add("branch", sip.GenerateBranch()).(sip.HeaderParams),
		}

		origin.PrependHeader(viaHop)
	}

	return origin
}

func (tx *ClientTx) Init() error {
	tx.initFSM()

	if err := tx.tpl.WriteMsg(tx.Origin()); err != nil {
		tx.mu.Lock()
		tx.lastErr = err
		tx.mu.Unlock()

		tx.spinFsm(client_input_transport_err)

		return err
	}

	if tx.reliable {
		tx.mu.Lock()
		tx.timer_d_time = 0
		tx.mu.Unlock()
	} else {
		// RFC 3261 valueWrite- 17.1.1.2.
		// If an unreliable transport is being used, the client transaction MUST start timer A with a value of T1.
		// If a reliable transport is being used, the client transaction SHOULD NOT
		// start timer A (Timer A controls request retransmissions).
		// Timer A - retransmission
		// tx.log.Tracef("timer_a set to %v", Timer_A)

		tx.mu.Lock()
		tx.timer_a_time = Timer_A

		tx.timer_a = timing.AfterFunc(tx.timer_a_time, func() {
			tx.spinFsm(client_input_timer_a)
		})
		// Timer D is set to 32 seconds for unreliable transports
		tx.timer_d_time = Timer_D
		tx.mu.Unlock()
	}

	// Timer B - timeout
	tx.mu.Lock()
	tx.timer_b = timing.AfterFunc(Timer_B, func() {
		tx.spinFsm(client_input_timer_b)
	})
	tx.mu.Unlock()

	tx.mu.RLock()
	err := tx.lastErr
	tx.mu.RUnlock()

	return err
}

func (tx *ClientTx) Receive(res *sip.Response) error {
	var input FsmInput
	if res.IsCancel() {
		input = client_input_canceled
	} else {
		tx.mu.Lock()
		tx.lastResp = res
		tx.mu.Unlock()

		switch {
		case res.IsProvisional():
			input = client_input_1xx
		case res.IsSuccess():
			input = client_input_2xx
		default:
			input = client_input_300_plus
		}
	}

	tx.spinFsm(input)
	return nil
}

func (tx *ClientTx) Responses() <-chan *sip.Response {
	return tx.responses
}

// Cancel cancels client transaction by sending CANCEL request
func (tx *ClientTx) Cancel() error {
	tx.spinFsm(client_input_cancel)
	return nil
}

func (tx *ClientTx) Terminate() {
	select {
	case <-tx.done:
		return
	default:
	}

	tx.delete()
}

func (tx *ClientTx) cancel() {
	if !tx.Origin().IsInvite() {
		return
	}

	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	cancelRequest := sip.NewCancelRequest(tx.Origin())
	if err := tx.tpl.WriteMsg(cancelRequest); err != nil {
		var lastRespStr string
		if lastResp != nil {
			lastRespStr = lastResp.Short()
		}
		tx.log.Error().
			Str("invite_request", tx.Origin().Short()).
			Str("invite_response", lastRespStr).
			Str("cancel_request", cancelRequest.Short()).
			Msgf("send CANCEL request failed: %s", err)

		tx.mu.Lock()
		tx.lastErr = err
		tx.mu.Unlock()

		go func() {
			tx.spinFsm(client_input_transport_err)
		}()
	}
}

func (tx *ClientTx) ack() {
	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	ack := sip.NewAckRequest(tx.Origin(), lastResp, nil)
	err := tx.tpl.WriteMsg(ack)
	if err != nil {
		tx.log.Error().
			Str("invite_request", tx.Origin().Short()).
			Str("invite_response", lastResp.Short()).
			Str("cancel_request", ack.Short()).
			Msgf("send ACK request failed: %s", err)

		tx.mu.Lock()
		tx.lastErr = err
		tx.mu.Unlock()

		go func() {
			tx.spinFsm(client_input_transport_err)
		}()
	}
}

// Initialises the correct kind of FSM based on request method.
func (tx *ClientTx) initFSM() {
	tx.fsmMu.Lock()
	if tx.Origin().IsInvite() {
		tx.fsmState = tx.inviteStateCalling
	} else {
		tx.fsmState = tx.stateCalling
	}
	tx.fsmMu.Unlock()
}

func (tx *ClientTx) resend() {
	select {
	case <-tx.done:
		return
	default:
	}

	// tx.log.Debug("resend origin request")

	err := tx.tpl.WriteMsg(tx.Origin())

	tx.mu.Lock()
	tx.lastErr = err
	tx.mu.Unlock()

	if err != nil {
		go func() {
			tx.spinFsm(client_input_transport_err)
		}()
	}
}

func (tx *ClientTx) passUp() {
	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	if lastResp != nil {
		select {
		case <-tx.done:
		case tx.responses <- lastResp:
		}
	}
}

func (tx *ClientTx) delete() {
	tx.closeOnce.Do(func() {
		tx.mu.Lock()

		close(tx.done)
		close(tx.responses)
		tx.mu.Unlock()

		// Maybe there is better way
		tx.onTerminate(tx.key)
	})

	time.Sleep(time.Microsecond)

	tx.mu.Lock()
	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}
	if tx.timer_d != nil {
		tx.timer_d.Stop()
		tx.timer_d = nil
	}
	tx.mu.Unlock()
}
