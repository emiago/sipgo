package transaction

import (
	"fmt"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"

	"github.com/rs/zerolog"
)

type ClientTx struct {
	commonTx
	responses    chan *sip.Response
	timer_a_time time.Duration // Current duration of timer A.
	timer_a      *time.Timer
	timer_b      *time.Timer
	timer_d_time time.Duration // Current duration of timer D.
	timer_d      *time.Timer
	timer_m      *time.Timer

	mu        sync.RWMutex
	closeOnce sync.Once
}

func NewClientTx(key string, origin *sip.Request, conn transport.Connection, logger zerolog.Logger) *ClientTx {
	tx := &ClientTx{}
	tx.key = key
	// tx.conn = tpl
	tx.conn = conn
	// buffer chan - about ~10 retransmit responses
	tx.responses = make(chan *sip.Response)
	tx.done = make(chan struct{})
	tx.log = logger

	tx.origin = origin
	return tx
}

func (tx *ClientTx) Init() error {
	tx.initFSM()

	if err := tx.conn.WriteMsg(tx.origin); err != nil {
		tx.log.Debug().Err(err).Str("req", tx.origin.StartLine()).Msg("Fail to write request on init")
		return wrapTransportError(err)
	}

	reliable := transport.IsReliable(tx.origin.Transport())
	if reliable {
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

		tx.timer_a = time.AfterFunc(tx.timer_a_time, func() {
			tx.spinFsm(client_input_timer_a)
		})
		// Timer D is set to 32 seconds for unreliable transports
		tx.timer_d_time = Timer_D
		tx.mu.Unlock()
	}

	// Timer B - timeout
	tx.mu.Lock()
	tx.timer_b = time.AfterFunc(Timer_B, func() {
		tx.mu.Lock()
		tx.lastErr = fmt.Errorf("Timer_B timed out. %w", ErrTimeout)
		tx.mu.Unlock()
		tx.spinFsm(client_input_timer_b)
	})
	tx.mu.Unlock()
	return nil
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

func (tx *ClientTx) Err() error {
	tx.mu.RLock()
	err := tx.lastErr
	tx.mu.RUnlock()
	return err
}

func (tx *ClientTx) cancel() {
	if !tx.origin.IsInvite() {
		return
	}

	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	cancelRequest := sip.NewCancelRequest(tx.origin)
	if err := tx.conn.WriteMsg(cancelRequest); err != nil {
		var lastRespStr string
		if lastResp != nil {
			lastRespStr = lastResp.Short()
		}
		tx.log.Error().
			Str("invite_request", tx.origin.Short()).
			Str("invite_response", lastRespStr).
			Str("cancel_request", cancelRequest.Short()).
			Msgf("send CANCEL request failed: %s", err)

		tx.mu.Lock()
		tx.lastErr = wrapTransportError(err)
		tx.mu.Unlock()

		go tx.spinFsm(client_input_transport_err)
	}
}

func (tx *ClientTx) ack() {
	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	ack := sip.NewAckRequest(tx.origin, lastResp, nil)
	err := tx.conn.WriteMsg(ack)
	if err != nil {
		tx.log.Error().
			Str("invite_request", tx.origin.Short()).
			Str("invite_response", lastResp.Short()).
			Str("cancel_request", ack.Short()).
			Msgf("send ACK request failed: %s", err)

		tx.mu.Lock()
		tx.lastErr = wrapTransportError(err)
		tx.mu.Unlock()

		go tx.spinFsm(client_input_transport_err)
	}
}

// Initialises the correct kind of FSM based on request method.
func (tx *ClientTx) initFSM() {
	tx.fsmMu.Lock()
	if tx.origin.IsInvite() {
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

	err := tx.conn.WriteMsg(tx.origin)
	if err != nil {
		tx.mu.Lock()
		tx.lastErr = wrapTransportError(err)
		tx.mu.Unlock()

		tx.log.Debug().Err(err).Str("req", tx.origin.StartLine()).Msg("Fail to resend request")
		go tx.spinFsm(client_input_transport_err)
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

		if _, err := tx.conn.TryClose(); err != nil {
			tx.log.Info().Err(err).Msg("Closing connection returned error")
		}
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
	tx.log.Debug().Str("tx", tx.Key()).Msg("Destroyed")
}
