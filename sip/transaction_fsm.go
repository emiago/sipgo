package sip

type fsmInput int
type fsmState func() fsmInput
type fsmContextState func(s fsmInput) fsmInput

const ()

// FSM States
const (
	client_state_calling = iota
	client_state_proceeding
	client_state_completed
	client_state_accepted
	client_state_terminated
)

// FSM States
const (
	server_state_trying = iota
	server_state_proceeding
	server_state_completed
	server_state_confirmed
	server_state_accepted
	server_state_terminated
)

// FSM Inputs
const (
	FsmInputNone fsmInput = iota
	// Server transaction inputs
	server_input_request
	server_input_ack
	server_input_cancel
	server_input_user_1xx
	server_input_user_2xx
	server_input_user_300_plus
	server_input_timer_g
	server_input_timer_h
	server_input_timer_i
	server_input_timer_j
	server_input_timer_l
	server_input_transport_err
	server_input_delete
	// Client transactions inputs
	client_input_1xx
	client_input_2xx
	client_input_300_plus
	client_input_timer_a
	client_input_timer_b
	client_input_timer_d
	client_input_timer_m
	client_input_transport_err
	client_input_delete
	client_input_cancel
	client_input_canceled
)

func fsmString(f fsmInput) string {
	switch f {
	case FsmInputNone:
		return "none"
	// Server transaction inputs
	case server_input_request:
		return "server_input_request"
	case server_input_ack:
		return "server_input_ack"
	case server_input_cancel:
		return "server_input_cancel"
	case server_input_user_1xx:
		return "server_input_user_1xx"
	case server_input_user_2xx:
		return "server_input_user_2xx"
	case server_input_user_300_plus:
		return "server_input_user_300_plus"
	case server_input_timer_g:
		return "server_input_timer_g"
	case server_input_timer_h:
		return "server_input_timer_h"
	case server_input_timer_i:
		return "server_input_timer_i"
	case server_input_timer_j:
		return "server_input_timer_j"
	case server_input_timer_l:
		return "server_input_timer_l"
	case server_input_transport_err:
		return "server_input_transport_err"
	case server_input_delete:
		return "server_input_delete"
		// Client transactions inputs
	case client_input_1xx:
		return "client_input_1xx"
	case client_input_2xx:
		return "client_input_2xx"
	case client_input_300_plus:
		return "client_input_300_plus"
	case client_input_timer_a:
		return "client_input_timer_a"
	case client_input_timer_b:
		return "client_input_timer_b"
	case client_input_timer_d:
		return "client_input_timer_d"
	case client_input_timer_m:
		return "client_input_timer_m"
	case client_input_transport_err:
		return "client_input_transport_err"
	case client_input_delete:
		return "client_input_delete"
	case client_input_cancel:
		return "client_input_cancel"
	case client_input_canceled:
		return "client_input_canceled"
	}
	return "unknown transaction state"
}
