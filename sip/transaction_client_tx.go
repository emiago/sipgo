package sip

import (
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type ClientTx struct {
	baseTx
	responses    chan *Response
	timer_a_time time.Duration // Current duration of timer A.
	timer_a      *time.Timer
	timer_b      *time.Timer
	timer_d_time time.Duration // Current duration of timer D.
	timer_d      *time.Timer
	timer_m      *time.Timer

	mu        sync.RWMutex
	closeOnce sync.Once
}

func NewClientTx(key string, origin *Request, conn Connection, logger zerolog.Logger) *ClientTx {
	tx := &ClientTx{}
	tx.key = key
	// tx.conn = tpl
	tx.conn = conn
	// buffer chan - about ~10 retransmit responses
	tx.responses = make(chan *Response)
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

	reliable := IsReliable(tx.origin.Transport())
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
		tx.spinFsmWithError(client_input_timer_b, fmt.Errorf("Timer_B timed out. %w", ErrTransactionTimeout))
	})
	tx.mu.Unlock()
	tx.log.Debug().Str("tx", tx.Key()).Msg("Client transaction initialized")
	return nil
}

// Initialises the correct kind of FSM based on request method.
func (tx *ClientTx) initFSM() {
	if tx.origin.IsInvite() {
		tx.baseTx.initFSM(tx.inviteStateCalling)
	} else {
		tx.baseTx.initFSM(tx.stateCalling)
	}
}

func (tx *ClientTx) Responses() <-chan *Response {
	return tx.responses
}

// Cancel cancels client transaction by sending CANCEL request
func (tx *ClientTx) Cancel() error {
	tx.spinFsm(client_input_cancel)
	return nil
}

func (tx *ClientTx) Terminate() {
	// select {
	// case <-tx.done:
	// 	return
	// default:
	// }

	tx.delete()
}

// Receive will process response in safe way and change transaction state
// NOTE: it could block while passing response to client,
// therefore running in seperate goroutine is needed
func (tx *ClientTx) Receive(res *Response) {
	var input fsmInput
	if res.IsCancel() {
		input = client_input_canceled
	} else {
		switch {
		case res.IsProvisional():
			input = client_input_1xx
		case res.IsSuccess():
			input = client_input_2xx
		default:
			input = client_input_300_plus
		}
	}

	tx.spinFsmWithResponse(input, res)
}

func (tx *ClientTx) cancel() {
	if !tx.origin.IsInvite() {
		return
	}

	cancelRequest := newCancelRequest(tx.origin)
	if err := tx.conn.WriteMsg(cancelRequest); err != nil {
		tx.log.Error().
			Str("invite_request", tx.origin.Short()).
			Str("cancel_request", cancelRequest.Short()).
			Msgf("send CANCEL request failed: %s", err)

		err := wrapTransportError(err)
		go tx.spinFsmWithError(client_input_transport_err, err)
	}
}

func (tx *ClientTx) ack() {
	resp := tx.fsmResp
	if resp == nil {
		panic("Response in ack should not be nil")
	}

	ack := newAckRequestNon2xx(tx.origin, resp, nil)
	err := tx.conn.WriteMsg(ack)
	if err != nil {
		tx.log.Error().
			Str("invite_request", tx.origin.Short()).
			Str("invite_response", resp.Short()).
			Str("cancel_request", ack.Short()).
			Msgf("send ACK request failed: %s", err)

		err := wrapTransportError(err)
		go tx.spinFsmWithError(client_input_transport_err, err)
	}
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
		tx.log.Debug().Err(err).Str("req", tx.origin.StartLine()).Msg("Fail to resend request")
		err := wrapTransportError(err)
		go tx.spinFsmWithError(client_input_transport_err, err)
	}
}

func (tx *ClientTx) delete() {
	tx.closeOnce.Do(func() {
		tx.mu.Lock()

		close(tx.done)
		tx.mu.Unlock()

		// Maybe there is better way
		if tx.onTerminate != nil {
			tx.onTerminate(tx.key)
		}

		if _, err := tx.conn.TryClose(); err != nil {
			tx.log.Info().Err(err).Msg("Closing connection returned error")
		}
	})

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
	tx.log.Debug().Str("tx", tx.Key()).Msg("Client transaction destroyed")
}
