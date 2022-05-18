package transaction

type FsmInput int
type FsmState func() FsmInput
type FsmContextState func(s FsmInput) FsmInput

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
	FsmInputNone FsmInput = iota
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
