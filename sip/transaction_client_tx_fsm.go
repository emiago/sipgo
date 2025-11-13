package sip

import (
	"time"
)

// TODO v2
// Better design could by passing some context through fsm state
// Context could carry either response or error

func (tx *ClientTx) inviteStateCalling(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case client_input_1xx:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actInviteProceeding
	case client_input_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupAccept
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actInviteFinal

		// NOTE
		// https://datatracker.ietf.org/doc/html/rfc3261#section-9.1
		// defines that no cancel should be sent unless we are in proceeding state
		// problematic part is wait
	// case client_input_cancel:
	// 	tx.fsmState, spinfn = tx.inviteStateCalling, tx.actCancel
	// case client_input_canceled:
	// 	tx.fsmState, spinfn = tx.inviteStateCalling, tx.actInviteCanceled
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
func (tx *ClientTx) inviteStateProcceeding(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case client_input_1xx:
		tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actPassup
	case client_input_2xx:
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupAccept
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actInviteFinal
	// case client_input_cancel:
	// 	tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actCancelTimeout
	// case client_input_canceled:
	// 	tx.fsmState, spinfn = tx.inviteStateProcceeding, tx.actInviteCanceled
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
func (tx *ClientTx) inviteStateCompleted(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case client_input_300_plus:
		tx.fsmState, spinfn = tx.inviteStateCompleted, tx.actAckResend
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

func (tx *ClientTx) inviteStateAccepted(s fsmInput) fsmInput {
	// https://datatracker.ietf.org/doc/html/rfc6026#section-7.2
	// Updated by RFC 6026
	// It modifies state transitions in the INVITE server state
	// machine to absorb retransmissions of the INVITE request after
	// encountering an unrecoverable transport error when sending a
	// response.  It also forbids forwarding stray responses to INVITE
	// requests (not just 2xx responses), which RFC 3261 requires.

	var spinfn fsmState
	switch s {
	case client_input_2xx:
		// 	If a 2xx response is
		//  received while the client INVITE state machine is in the "Calling" or
		//  "Proceeding" states, it MUST transition to the "Accepted" state, pass
		//  the 2xx response to the TU, and set Timer M to 64*T1
		tx.log.Debug("retransimission 2xx detected", "tx", tx.Key())
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actPassupRetransmission

	case client_input_transport_err:
		tx.log.Warn("client transport error detected. Waiting for retransmission", "tx", tx.Key())
		tx.fsmState, spinfn = tx.inviteStateAccepted, tx.actTranErrNoDelete
	case client_input_timer_m:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

// Terminated
func (tx *ClientTx) inviteStateTerminated(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case client_input_delete:
		tx.fsmState, spinfn = tx.inviteStateTerminated, tx.actDelete
	default:
		// No changes
		return FsmInputNone
	}
	return spinfn()
}

func (tx *ClientTx) stateCalling(s fsmInput) fsmInput {
	var spinfn fsmState
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
func (tx *ClientTx) stateProceeding(s fsmInput) fsmInput {
	var spinfn fsmState
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
func (tx *ClientTx) stateCompleted(s fsmInput) fsmInput {
	var spinfn fsmState
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
func (tx *ClientTx) stateTerminated(s fsmInput) fsmInput {
	var spinfn fsmState
	switch s {
	case client_input_delete:
		tx.fsmState, spinfn = tx.stateTerminated, tx.actDelete
	default:
		return FsmInputNone
	}
	return spinfn()
}

// Define actions
func (tx *ClientTx) actInviteResend() fsmInput {
	tx.mu.Lock()

	tx.timer_a_time *= 2
	tx.timer_a.Reset(tx.timer_a_time)

	tx.mu.Unlock()

	tx.resend()

	return FsmInputNone
}

func (tx *ClientTx) actInviteCanceled() fsmInput {
	// nothing to do here for now
	return FsmInputNone
}

func (tx *ClientTx) actResend() fsmInput {
	// tx.Log().Debug("actResend")

	tx.mu.Lock()

	tx.timer_a_time *= 2
	// For non-INVITE, cap timer A at T2 seconds.
	if tx.timer_a_time > T2 {
		tx.timer_a_time = T2
	}

	if tx.timer_a != nil {
		tx.timer_a.Reset(tx.timer_a_time)
	}

	tx.mu.Unlock()

	tx.resend()

	return FsmInputNone
}

func (tx *ClientTx) actInviteProceeding() fsmInput {
	// tx.Log().Debug("actInviteProceeding")

	tx.fsmPassUp()

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

func (tx *ClientTx) actInviteFinal() fsmInput {

	tx.ack()
	tx.fsmPassUp()

	tx.mu.Lock()

	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}

	tx.timer_d = time.AfterFunc(tx.timer_d_time, func() {
		tx.spinFsm(client_input_timer_d)
	})

	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actFinal() fsmInput {
	// tx.Log().Debug("actFinal")

	tx.fsmPassUp()

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

// func (tx *ClientTx) actCancel() fsmInput {
// 	// tx.Log().Debug("actCancel")

// 	tx.cancel()

// 	return FsmInputNone
// }

// func (tx *ClientTx) actCancelTimeout() fsmInput {
// 	// tx.Log().Debug("actCancel")

// 	tx.cancel()

// 	// tx.Log().Tracef("timer_b set to %v", Timer_B)

// 	tx.mu.Lock()
// 	if tx.timer_b != nil {
// 		tx.timer_b.Stop()
// 	}
// 	tx.timer_b = time.AfterFunc(Timer_B, func() {
// 		tx.spinFsm(client_input_timer_b)
// 	})
// 	tx.mu.Unlock()

// 	return FsmInputNone
// }

func (tx *ClientTx) actAckResend() fsmInput {
	// Detect ACK loop.
	// Case ACK sent and response is received
	if tx.fsmAck != nil {
		// ACK was sent. Now delay to prevent infinite loop as temporarly fix
		// This is not clear per RFC, but client could generate a lot requests in this case
		tx.log.Error("ACK loop retransimission. Resending after T2", "tx", tx.Key())
		select {
		case <-tx.done:
			return FsmInputNone
		case <-time.After(T2):
		}
	}
	tx.ack()

	return FsmInputNone
}

func (tx *ClientTx) actTransErr() fsmInput {
	tx.stopTimerA()
	return client_input_delete
}

func (tx *ClientTx) actTranErrNoDelete() fsmInput {
	tx.actTransErr()
	return FsmInputNone
}

func (tx *ClientTx) actTimeout() fsmInput {
	tx.stopTimerA()
	return client_input_delete
}

func (tx *ClientTx) actPassup() fsmInput {
	tx.fsmPassUp()
	tx.stopTimerA()
	return FsmInputNone
}

func (tx *ClientTx) actPassupRetransmission() fsmInput {
	tx.passUpRetransmission()
	return FsmInputNone
}

func (tx *ClientTx) actPassupDelete() fsmInput {
	tx.fsmPassUp()
	tx.stopTimerA()
	return client_input_delete
}

func (tx *ClientTx) actPassupAccept() fsmInput {
	tx.fsmPassUp()

	tx.mu.Lock()
	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
		tx.timer_b = nil
	}

	tx.timer_m = time.AfterFunc(Timer_M, func() {
		tx.spinFsm(client_input_timer_m)
	})
	tx.mu.Unlock()

	return FsmInputNone
}

func (tx *ClientTx) actDelete() fsmInput {
	if tx.fsmErr == nil {
		tx.fsmErr = ErrTransactionTerminated
	}
	tx.delete(tx.fsmErr)
	return FsmInputNone
}

func (tx *ClientTx) stopTimerA() {
	tx.mu.Lock()
	if tx.timer_a != nil {
		tx.timer_a.Stop()
		tx.timer_a = nil
	}
	tx.mu.Unlock()
}

func (tx *ClientTx) fsmPassUp() {
	lastResp := tx.fsmResp

	if lastResp == nil {
		return
	}

	select {
	case <-tx.done:
	case tx.responses <- lastResp:
	}
}

func (tx *ClientTx) passUpRetransmission() {
	// RFC 6026 handling retransmissions
	lastResp := tx.fsmResp

	if lastResp == nil {
		return
	}

	// Only hook based should handle retransmission
	tx.mu.Lock()
	onResp := tx.onRetransmission
	tx.mu.Unlock()

	// To consider: passing via hook can be better to avoid deadlock
	if onResp != nil {
		tx.fsmMu.Unlock() // Avoids potential deadlock
		onResp(lastResp)
		tx.fsmMu.Lock()
		return
	}

	tx.log.Debug("skipped response. Retransimission", "tx", tx.Key())

	// Client probably left or not interested, so therefore we must not block here
	// For proxies they should handle this retransmission
}
