// Originally forked from https://github.com/ghettovoice/gosip by @ghetovoice
package transaction

import (
	"fmt"
	"time"
)

// invite state machine https://datatracker.ietf.org/doc/html/rfc3261#section-17.1.1.2
// TODO needs to be refactored
func (tx *ServerTx) inviteStateProcceeding(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case server_input_request:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actRespond
	case server_input_cancel:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actCancel
	case server_input_user_1xx:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actRespond
	case server_input_user_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actRespondAccept
	case server_input_user_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actRespondComplete
	case server_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTransErr
	}

	return spinfn()
}

func (tx *ServerTx) inviteStateCompleted(s FsmInput) FsmInput {
	var spinfn FsmState
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

func (tx *ServerTx) inviteStateConfirmed(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case server_input_timer_i:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) inviteStateAccepted(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case server_input_ack:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupAck
	case server_input_user_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actRespond
	case server_input_timer_l:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) inviteStateTerminated(s FsmInput) FsmInput {
	var spinfn FsmState
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

func (tx *ServerTx) stateTrying(s FsmInput) FsmInput {
	var spinfn FsmState
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
func (tx *ServerTx) stateProceeding(s FsmInput) FsmInput {
	var spinfn FsmState
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
func (tx *ServerTx) stateCompleted(s FsmInput) FsmInput {
	var spinfn FsmState
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
func (tx *ServerTx) stateTerminated(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case server_input_delete:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ServerTx) actRespond() FsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	return FsmInputNone
}

func (tx *ServerTx) actRespondComplete() FsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	if !tx.reliable {
		tx.mu.Lock()
		if tx.timer_g == nil {
			// tx.Log().Tracef("timer_g set to %v", tx.timer_g_time)

			tx.timer_g = time.AfterFunc(tx.timer_g_time, func() {
				// tx.Log().Trace("timer_g fired")
				tx.spinFsm(server_input_timer_g)
			})
		} else {
			tx.timer_g_time *= 2
			if tx.timer_g_time > T2 {
				tx.timer_g_time = T2
			}

			// tx.Log().Tracef("timer_g reset to %v", tx.timer_g_time)

			tx.timer_g.Reset(tx.timer_g_time)
		}
		tx.mu.Unlock()
	}

	tx.mu.Lock()
	if tx.timer_h == nil {
		tx.timer_h = time.AfterFunc(Timer_H, func() {
			// tx.Log().Trace("timer_h fired")
			tx.spinFsm(server_input_timer_h)
		})
	}
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ServerTx) actRespondAccept() FsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	tx.mu.Lock()
	// tx.Log().Tracef("timer_l set to %v", Timer_L)
	tx.timer_l = time.AfterFunc(Timer_L, func() {
		// tx.Log().Trace("timer_l fired")
		tx.spinFsm(server_input_timer_l)
	})
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ServerTx) actPassupAck() FsmInput {
	tx.passAck()
	return FsmInputNone
}

// Send final response
func (tx *ServerTx) actFinal() FsmInput {
	if err := tx.passResp(); err != nil {
		return server_input_transport_err
	}

	tx.mu.Lock()
	tx.timer_j = time.AfterFunc(Timer_J, func() {
		// tx.Log().Trace("timer_j fired")
		tx.spinFsm(server_input_timer_j)
	})

	tx.mu.Unlock()

	return FsmInputNone
}

// Inform user of transport error
func (tx *ServerTx) actTransErr() FsmInput {
	// tx.Log().Debug("actTrans_err")

	tx.transportErr()

	return server_input_delete
}

// Inform user of timeout error
func (tx *ServerTx) actTimeout() FsmInput {
	// tx.Log().Debug("actTimeout")

	tx.timeoutErr()

	return server_input_delete
}

// Just delete the transaction.
func (tx *ServerTx) actDelete() FsmInput {
	// tx.Log().Debug("actDelete")

	tx.delete()

	return FsmInputNone
}

// Send response and delete the transaction.
func (tx *ServerTx) actRespondDelete() FsmInput {
	// tx.Log().Debug("actRespondDelete")
	tx.delete()
	lastErr := tx.conn.WriteMsg(tx.lastResp)

	tx.mu.Lock()
	tx.lastErr = lastErr
	tx.mu.Unlock()

	if lastErr != nil {
		tx.log.Debug().Err(lastErr).Msg("fail to actRespondDelete")
		return server_input_transport_err
	}

	return FsmInputNone
}

func (tx *ServerTx) actConfirm() FsmInput {
	// tx.Log().Debug("actConfirm")

	// todo bloody patch
	// defer func() { recover() }()

	tx.mu.Lock()

	if tx.timer_g != nil {
		tx.timer_g.Stop()
		tx.timer_g = nil
	}

	if tx.timer_h != nil {
		tx.timer_h.Stop()
		tx.timer_h = nil
	}

	// tx.Log().Tracef("timer_i set to %v", Timer_I)

	tx.timer_i = time.AfterFunc(Timer_I, func() {
		// tx.Log().Trace("timer_i fired")
		tx.spinFsm(server_input_timer_i)
	})

	tx.mu.Unlock()

	tx.passAck()
	return FsmInputNone
}

func (tx *ServerTx) actCancel() FsmInput {
	tx.passCancel()
	return FsmInputNone
}

func (tx *ServerTx) transportErr() {
	tx.mu.RLock()
	err := tx.lastErr
	tx.mu.RUnlock()

	err = fmt.Errorf("transaction failed to send %s: %w", tx.key, err)

	go tx.sendErr(err)
}

func (tx *ServerTx) timeoutErr() {
	err := fmt.Errorf("transaction timed out")
	go tx.sendErr(err)
}
