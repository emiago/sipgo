package sip

import (
	"time"
)

// TODO v2
// Originally forked from https://github.com/ghettovoice/gosip by @ghetovoice
// Better design could by passing some context through fsm state
// Context could carry either response or error

// invite state machine https://datatracker.ietf.org/doc/html/rfc3261#section-17.1.1.2
func (tx *ServerTx) inviteStateProcceeding(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_request:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actRespond
	case server_input_cancel:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actCancel
	case server_input_user_1xx:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actRespond
	case server_input_user_2xx:
		// https://www.rfc-editor.org/rfc/rfc6026#section-7.1
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actRespondAccept
	case server_input_user_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actRespondComplete
	case server_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTransErr
	default:
		return FsmInputNone
	}

	return spinfn()
}

func (tx *ServerTx) inviteStateCompleted(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_request:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actRespond
	case server_input_ack:
		tx.fsmState, spinfn = tx.inviteStateConfirmed, tx.actConfirm
	case server_input_timer_g:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actRespondComplete
	case server_input_timer_h:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	case server_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTransErr
	default:
		// No changes
		return FsmInputNone
	}

	return spinfn()
}

func (tx *ServerTx) inviteStateConfirmed(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_timer_i:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) inviteStateAccepted(s fsmInput) fsmInput {
	// https://www.rfc-editor.org/rfc/rfc6026#section-7.1
	var spinfn fsmState
	switch s {
	case server_input_ack:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupAck
	case server_input_user_2xx:
		// The server transaction MUST NOT generate 2xx retransmissions on its
		// own.  Any retransmission of the 2xx response passed from the TU to
		// the transaction while in the "Accepted" state MUST be passed to the
		// transport layer for transmission.
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actRespond
	case server_input_timer_l:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) inviteStateTerminated(s fsmInput) fsmInput {
	var spinfn fsmState
	// Terminated
	switch s {
	case server_input_delete:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) stateTrying(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_user_1xx:
		tx.fsmState, spinfn = tx.stateProceeding, tx.actRespond
	case server_input_user_2xx:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case server_input_user_300_plus:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case server_input_transport_err:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTransErr
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

// Proceeding
func (tx *ServerTx) stateProceeding(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_request:
		tx.fsmState, spinfn = tx.stateProceeding, tx.actRespond
	case server_input_user_1xx:
		tx.fsmState, spinfn = tx.stateProceeding, tx.actRespond
	case server_input_user_2xx:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case server_input_user_300_plus:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case server_input_transport_err:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTransErr
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

// Completed
func (tx *ServerTx) stateCompleted(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_request:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actRespond
	case server_input_timer_j:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	case server_input_transport_err:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTransErr
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

// Terminated
func (tx *ServerTx) stateTerminated(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case server_input_delete:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) actRespond() fsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	return FsmInputNone
}

func (tx *ServerTx) actRespondComplete() fsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	if !tx.reliable {
		tx.mu.Lock()
		if tx.timer_g == nil {

			tx.timer_g = time.AfterFunc(tx.timer_g_time, func() {
				tx.spinFsm(server_input_timer_g)
			})
		} else {
			tx.timer_g_time *= 2
			if tx.timer_g_time > T2 {
				tx.timer_g_time = T2
			}

			tx.timer_g.Reset(tx.timer_g_time)
		}
		tx.mu.Unlock()
	}

	tx.mu.Lock()
	if tx.timer_h == nil {
		tx.timer_h = time.AfterFunc(Timer_H, func() {
			tx.spinFsm(server_input_timer_h)
		})
	}
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ServerTx) actRespondAccept() fsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	tx.mu.Lock()
	tx.timer_l = time.AfterFunc(Timer_L, func() {
		tx.spinFsm(server_input_timer_l)
	})
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ServerTx) actPassupAck() fsmInput {
	tx.passAck()
	return FsmInputNone
}

// Send final response
func (tx *ServerTx) actFinal() fsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	// https://datatracker.ietf.org/doc/html/rfc3261#section-17.2.2
	//  When the server transaction enters the "Completed" state, it MUST set
	//    Timer J to fire in 64*T1 seconds for unreliable transports, and zero
	//    seconds for reliable transports.
	tx.mu.Lock()
	tx.timer_j = time.AfterFunc(tx.timer_j_time, func() {
		tx.spinFsm(server_input_timer_j)
	})
	tx.mu.Unlock()

	return FsmInputNone
}

// Inform user of transport error
func (tx *ServerTx) actTransErr() fsmInput {
	tx.log.Debug("Transport error. Transaction will terminate", "fsmError", tx.fsmErr, "tx", tx.Key())
	return server_input_delete
}

// Inform user of timeout fsmError
func (tx *ServerTx) actTimeout() fsmInput {
	tx.log.Debug("Timed out. Transaction will terminate", "fsmError", tx.fsmErr, "tx", tx.Key())
	return server_input_delete
}

// Just delete the transaction.
func (tx *ServerTx) actDelete() fsmInput {
	if tx.fsmErr == nil {
		tx.fsmErr = ErrTransactionTerminated
	}
	tx.delete(tx.fsmErr)
	return FsmInputNone
}

func (tx *ServerTx) actConfirm() fsmInput {
	tx.mu.Lock()

	if tx.timer_g != nil {
		tx.timer_g.Stop()
		tx.timer_g = nil
	}

	if tx.timer_h != nil {
		tx.timer_h.Stop()
		tx.timer_h = nil
	}

	// If transport is reliable this will be 0 and fire imediately
	tx.timer_i = time.AfterFunc(tx.timer_i_time, func() {
		tx.spinFsm(server_input_timer_i)
	})

	tx.mu.Unlock()

	tx.passAck()
	return FsmInputNone
}

func (tx *ServerTx) actCancel() fsmInput {
	r := tx.fsmCancel

	if r == nil {
		return FsmInputNone
	}

	tx.log.Debug("Passing 487 on CANCEL", "tx", tx.Key())
	tx.fsmResp = NewResponseFromRequest(tx.origin, StatusRequestTerminated, "Request Terminated", nil)
	tx.fsmErr = ErrTransactionCanceled // For now only informative

	// Check is there some listener on cancel
	tx.mu.Lock()
	onCancel := tx.onCancel
	tx.mu.Unlock()
	if onCancel != nil {
		onCancel(r)
	}

	return server_input_user_300_plus
}

func (tx *ServerTx) passAck() {
	r := tx.fsmAck
	if r == nil {
		return
	}

	tx.ackSendAsync(r)
}

func (tx *ServerTx) passResp() error {
	lastResp := tx.fsmResp

	if lastResp == nil {
		// We may have received multiple request but without any response
		// placed yet in transaction
		return nil
	}

	err := tx.conn.WriteMsg(lastResp)
	if err != nil {
		tx.log.Debug("fail to pass response", "error", err, "res", lastResp.StartLine(), "tx", tx.Key())
		tx.fsmErr = wrapTransportError(err)
		return err
	}
	return nil
}
