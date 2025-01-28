package sip

import (
	"fmt"
	"log/slog"
	"time"
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

	onRetransmission FnTxResponse
}

func NewClientTx(key string, origin *Request, conn Connection, logger *slog.Logger) *ClientTx {
	tx := &ClientTx{}
	tx.key = key
	// tx.conn = tpl
	tx.conn = conn
	// buffer chan - about ~10 retransmit responses
	tx.responses = make(chan *Response)
	tx.done = make(chan struct{})
	tx.log = logger

	tx.origin = origin // TODO:Due to subsequent request like ack we need to use clone to avoid races
	return tx
}

func (tx *ClientTx) Init() error {
	tx.initFSM()

	if err := tx.conn.WriteMsg(tx.origin); err != nil {
		e := fmt.Errorf("fail to write request on init req=%q: %w", tx.origin.StartLine(), err)
		return wrapTransportError(e)
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
	tx.log.Debug("Client transaction initialized", "tx", tx.Key())
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

func (tx *ClientTx) OnRetransmission(f FnTxResponse) bool {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return false
	}
	tx.registerOnResponse(f)
	tx.mu.Unlock()
	return true
}

func (tx *ClientTx) registerOnResponse(f FnTxResponse) {
	if tx.onRetransmission != nil {
		prev := tx.onRetransmission
		tx.onRetransmission = func(r *Response) {
			prev(r)
			f(r)
		}
		return
	}
	tx.onRetransmission = f
}

// Cancel cancels client transaction by sending CANCEL request
// func (tx *ClientTx) Cancel() error {
// 	tx.spinFsm(client_input_cancel)
// 	return nil
// }

func (tx *ClientTx) Terminate() {
	// select {
	// case <-tx.done:
	// 	return
	// default:
	// }

	if tx.delete(ErrTransactionTerminated) {
		tx.fsmMu.Lock()
		tx.fsmErr = ErrTransactionCanceled
		tx.fsmMu.Unlock()
	}
}

// Receive will process response in safe way and change transaction state
// NOTE: it could block while passing response to client,
// therefore running in seperate goroutine is needed
func (tx *ClientTx) Receive(res *Response) {
	var input fsmInput

	// There is no more client cancelation on transaction. It must be done by caller with seperate CANCEL request
	// and termination of current
	// if res.IsCancel() {
	// 	input = client_input_canceled
	// } else {
	switch {
	case res.IsProvisional():
		input = client_input_1xx
	case res.IsSuccess():
		input = client_input_2xx
	default:
		input = client_input_300_plus
	}
	// }

	tx.spinFsmWithResponse(input, res)
}

func (tx *ClientTx) Connection() Connection {
	return tx.conn
}

// func (tx *ClientTx) cancel() {
// 	if !tx.origin.IsInvite() {
// 		return
// 	}

// 	cancelRequest := newCancelRequest(tx.origin)
// 	if err := tx.conn.WriteMsg(cancelRequest); err != nil {
// 		tx.log.Error().
// 			Str("invite_request", tx.origin.Short()).
// 			Str("cancel_request", cancelRequest.Short()).
// 			Msgf("send CANCEL request failed: %s", err)

// 		err := wrapTransportError(err)
// 		go tx.spinFsmWithError(client_input_transport_err, err)
// 	}
// }

func (tx *ClientTx) ack() {
	resp := tx.fsmResp
	if resp == nil {
		panic("Response in ack should not be nil")
	}

	ack := newAckRequestNon2xx(tx.origin, resp, nil)
	tx.fsmAck = ack // NOTE: this could be incorect property to use but it helps preventing loops in some cases

	// https://github.com/emiago/sipgo/issues/168
	// Destination can be FQDN and we do not want to resolve this.
	// Per https://datatracker.ietf.org/doc/html/rfc3261#section-17.1.1.2
	// The ACK MUST be sent to the same address, port, and transport to which the original request was sent
	// This is only needed for UDP
	ack.raddr = tx.origin.raddr

	err := tx.conn.WriteMsg(ack)
	if err != nil {
		tx.log.Error("send ACK request failed", "tx", tx.Key(),
			slog.String("invite_request", tx.origin.Short()),
			slog.String("invite_response", resp.Short()),
			slog.String("cancel_request", ack.Short()),
		)
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
		tx.log.Debug("Fail to resend request", "error", err, "req", tx.origin.StartLine())
		err := wrapTransportError(err)
		go tx.spinFsmWithError(client_input_transport_err, err)
	}
}

func (tx *ClientTx) delete(err error) bool {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return false
	}
	tx.closed = true

	close(tx.done)
	onterm := tx.onTerminate

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
	// Maybe there is better way
	if onterm != nil {
		tx.onTerminate(tx.key, err)
	}

	if _, err := tx.conn.TryClose(); err != nil {
		tx.log.Info("Closing connection returned error", "error", err, "tx", tx.Key())
	}
	tx.log.Debug("Client transaction destroyed", "tx", tx.Key())
	return true
}
