package transaction

import (
	"fmt"
	"time"
)

func (tx *ClientTx) inviteStateCalling(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_1xx:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actInviteProceeding
	case client_input_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupAccept
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actInviteFinal
	case client_input_cancel:
		tx.fsmState, spinfn = tx.inviteStateCalling, tx.actCancel
	case client_input_canceled:
		tx.fsmState, spinfn = tx.inviteStateCalling, tx.actInviteCanceled
	case client_input_timer_a:
		tx.fsmState, spinfn = tx.inviteStateCalling, tx.actInviteResend
	case client_input_timer_b:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTimeout
	case client_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTransErr
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

// Proceeding
func (tx *ClientTx) inviteStateProcceeding(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_1xx:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actPassup
	case client_input_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupAccept
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actInviteFinal
	case client_input_cancel:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actCancelTimeout
	case client_input_canceled:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actInviteCanceled
	case client_input_timer_b:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTimeout
	case client_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTransErr
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

// Completed
func (tx *ClientTx) inviteStateCompleted(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actAck
	case client_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actTransErr
	case client_input_timer_d:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ClientTx) inviteStateAccepted(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassup
	case client_input_transport_err:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actTranErrNoDelete
	case client_input_timer_m:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ClientTx) actTranErrNoDelete() FsmInput {
	tx.actTransErr()
	return FsmInputNone
}

// Terminated
func (tx *ClientTx) inviteStateTerminated(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_delete:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ClientTx) stateCalling(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_1xx:
		tx.fsmState, spinfn = tx.stateProceeding, tx.actPassup
	case client_input_2xx:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case client_input_timer_a:
		tx.fsmState, spinfn = tx.stateCalling, tx.actResend
	case client_input_timer_b:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTimeout
	case client_input_transport_err:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTransErr
	default:
		return FsmInputNone
	}
	return spinfn()
}

// Proceeding
func (tx *ClientTx) stateProceeding(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_1xx:
		tx.fsmState, spinfn = tx.stateProceeding, tx.actPassup
	case client_input_2xx:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.stateCompleted, tx.actFinal
	case client_input_timer_a:
		tx.fsmState, spinfn = tx.stateProceeding, tx.actResend
	case client_input_timer_b:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTimeout
	case client_input_transport_err:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actTransErr
	default:
		return FsmInputNone
	}
	return spinfn()
}

// Completed
func (tx *ClientTx) stateCompleted(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_delete:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	case client_input_timer_d:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	default:
		return FsmInputNone
	}
	return spinfn()
}

// Terminated
func (tx *ClientTx) stateTerminated(s FsmInput) FsmInput {
	var spinfn FsmState
	switch s {
	case client_input_delete:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	default:
		return FsmInputNone
	}
	return spinfn()
}

// Define actions
func (tx *ClientTx) actInviteResend() FsmInput {
	tx.mu.Lock()

	tx.timer_a_time *= 2
	tx.timer_a.Reset(tx.timer_a_time)

	tx.mu.Unlock()

	tx.resend()

	return FsmInputNone
}

func (tx *ClientTx) actInviteCanceled() FsmInput {
	// nothing to do here for now
	return FsmInputNone
}

func (tx *ClientTx) actResend() FsmInput {
	// tx.Log().Debug("actResend")

	tx.mu.Lock()

	tx.timer_a_time *= 2
	// For non-INVITE, cap timer A at T2 seconds.
	if tx.timer_a_time > T2 {
		tx.timer_a_time = T2
	}
	tx.timer_a.Reset(tx.timer_a_time)

	tx.mu.Unlock()

	tx.resend()

	return FsmInputNone
}

func (tx *ClientTx) actPassup() FsmInput {
	// tx.Log().Debug("actPassup")

	tx.passUp()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}

	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actInviteProceeding() FsmInput {
	// tx.Log().Debug("actInviteProceeding")

	tx.passUp()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}

	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actInviteFinal() FsmInput {
	// tx.Log().Debug("actInviteFinal")

	tx.ack()
	tx.passUp()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}

	// tx.Log().Tracef("timer_d set to %v", tx.timer_d_time)

	tx.timer_d = time.AfterFunc(tx.timer_d_time, func() {
		tx.spinFsm(client_input_timer_d)
	})

	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actFinal() FsmInput {
	// tx.Log().Debug("actFinal")

	tx.passUp()

	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}

	// tx.Log().Tracef("timer_d set to %v", tx.timer_d_time)
	if tx.timer_d_time > 0 {
		tx.timer_d = time.AfterFunc(tx.timer_d_time, func() {
			tx.spinFsm(client_input_timer_d)
		})
		return FsmInputNone
	}

	return client_input_delete
}

func (tx *ClientTx) actCancel() FsmInput {
	// tx.Log().Debug("actCancel")

	tx.cancel()

	return FsmInputNone
}

func (tx *ClientTx) actCancelTimeout() FsmInput {
	// tx.Log().Debug("actCancel")

	tx.cancel()

	// tx.Log().Tracef("timer_b set to %v", Timer_B)

	tx.mu.Lock()
	if tx.timer_b != nil {
		tx.timer_b.Stop()
	}
	tx.timer_b = time.AfterFunc(Timer_B, func() {
		tx.spinFsm(client_input_timer_b)
	})
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actAck() FsmInput {
	// tx.Log().Debug("actAck")

	tx.ack()

	return FsmInputNone
}

func (tx *ClientTx) actTransErr() FsmInput {
	// tx.Log().Debug("actTransErr")

	tx.transportErr()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}

	tx.mu.Unlock()

	return client_input_delete
}

func (tx *ClientTx) actTimeout() FsmInput {
	// tx.Log().Debug("actTimeout")

	tx.timeoutErr()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}

	tx.mu.Unlock()

	return client_input_delete
}

func (tx *ClientTx) actPassupDelete() FsmInput {
	// tx.Log().Debug("actPassupDelete")

	tx.passUp()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}

	tx.mu.Unlock()

	return client_input_delete
}

func (tx *ClientTx) actPassupAccept() FsmInput {
	// tx.Log().Debug("actPassupAccept")

	tx.passUp()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}

	// tx.Log().Tracef("timer_m set to %v", Timer_M)

	tx.timer_m = time.AfterFunc(Timer_M, func() {
		select {
		case <-tx.done:
			return
		default:
		}

		// tx.Log().Trace("timer_m fired")

		tx.spinFsm(client_input_timer_m)
	})
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actDelete() FsmInput {
	// tx.Log().Debug("actDelete")

	tx.delete()

	return FsmInputNone
}

func (tx *ClientTx) transportErr() {
	tx.mu.RLock()
	err := tx.lastErr
	tx.mu.RUnlock()

	err = fmt.Errorf("transaction failed to send %s: %w", tx.origin.Short(), err)
	select {
	case <-tx.done:
	case tx.errs <- err:
	}
}

func (tx *ClientTx) timeoutErr() {
	err := fmt.Errorf("transaction timed out tx=%s", tx.key)

	select {
	case <-tx.done:
	case tx.errs <- err:
	}
}
