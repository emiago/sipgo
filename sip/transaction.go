package sip

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	// SIP timers are exposed for manipulation but best approach is using SetTimers
	// where all timers get populated based on
	// T1: Round-trip time (RTT) estimate, Default 500ms
	T1,
	// T2: Maximum retransmission interval for non-INVITE requests and INVITE responses
	T2,
	// T4: Maximum duration that a message can remain in the network1
	T4,
	// Timer_A controls sender request retransmissions for unreliable transports like UDP. It is incriesed 2x for every failure.
	Timer_A,
	// Timer_B (64 * T1) is the maximum amount of time that a sender will wait for an INVITE message to be acknowledged
	Timer_B,
	Timer_D,
	Timer_E,
	// Timer F is the maximum amount of time that a sender will wait for a non INVITE message to be acknowledged
	Timer_F,
	Timer_G,
	Timer_H,
	Timer_I,
	Timer_J,
	Timer_K,
	Timer_L,
	Timer_M time.Duration

	Timer_1xx = 200 * time.Millisecond

	TxSeperator = "__"
)

func init() {
	t1 := 500 * time.Millisecond
	t2 := 4 * time.Second
	t4 := 5 * time.Second
	SetTimers(t1, t2, t4)
}

func SetTimers(t1, t2, t4 time.Duration) {
	T1 = t1
	T2 = t2
	T4 = t4
	Timer_A = T1
	Timer_B = 64 * T1
	Timer_D = 32 * time.Second
	Timer_E = T1
	Timer_F = 64 * T1
	Timer_G = T1
	Timer_H = 64 * T1
	Timer_I = T4
	Timer_J = 64 * T1
	Timer_K = T4
	Timer_L = 64 * T1
	Timer_M = 64 * T1
}

var (
	// Transaction Layer Errors can be detected and handled with different response on caller side
	// https://www.rfc-editor.org/rfc/rfc3261#section-8.1.3.1
	ErrTransactionTimeout   = errors.New("transaction timeout")
	ErrTransactionTransport = errors.New("transaction transport error")
)

func wrapTransportError(err error) error {
	return fmt.Errorf("%s. %w", err.Error(), ErrTransactionTransport)
}

func wrapTimeoutError(err error) error {
	return fmt.Errorf("%s. %w", err.Error(), ErrTransactionTimeout)
}

type Transaction interface {
	// Terminate will terminate transaction
	Terminate()
	// Done when transaction fsm terminates. Can be called multiple times
	Done() <-chan struct{}
	// Last error. Useful to check when transaction terminates
	Err() error
}

type ServerTransaction interface {
	Transaction
	Respond(res *Response) error
	Acks() <-chan *Request
	// Cancels is triggered when transaction is canceled, that is SIP CANCEL is received for transaction.
	Cancels() <-chan *Request
}

type ClientTransaction interface {
	Transaction
	// Responses returns channel with all responses for transaction
	Responses() <-chan *Response
	// Cancel sends cancel request
	// TODO: Do we need context passing here?
	Cancel() error
}

type commonTx struct {
	key string

	origin *Request
	// tpl    *transport.Layer

	conn     Connection
	lastResp *Response

	lastErr error
	done    chan struct{}

	//State machine control
	fsmMu    sync.RWMutex
	fsmState fsmContextState

	log         zerolog.Logger
	onTerminate FnTxTerminate
}

func (tx *commonTx) String() string {
	if tx == nil {
		return "<nil>"
	}

	return tx.key
}

func (tx *commonTx) Origin() *Request {
	return tx.origin
}

func (tx *commonTx) Key() string {
	return tx.key
}

func (tx *commonTx) Done() <-chan struct{} {
	return tx.done
}

func (tx *commonTx) OnTerminate(f FnTxTerminate) {
	tx.onTerminate = f
}

func (tx *commonTx) currentFsmState() fsmContextState {
	tx.fsmMu.Lock()
	defer tx.fsmMu.Unlock()
	return tx.fsmState
}

// Choose the right FSM init function depending on request method.
func (tx *commonTx) spinFsm(in fsmInput) {
	tx.fsmMu.Lock()
	for i := in; i != FsmInputNone; {
		i = tx.fsmState(i)
	}
	tx.fsmMu.Unlock()
}

type FnTxTerminate func(key string)

// MakeServerTxKey creates server key for matching retransmitting requests - RFC 3261 17.2.3.
func MakeServerTxKey(msg Message) (string, error) {
	firstViaHop := msg.Via()
	if firstViaHop == nil {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", MessageShortString(msg))
	}

	cseq := msg.CSeq()
	if cseq == nil {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", MessageShortString(msg))
	}
	method := cseq.MethodName
	if method == ACK || method == CANCEL {
		method = INVITE
	}

	var isRFC3261 bool
	branch, ok := firstViaHop.Params.Get("branch")
	if ok && branch != "" &&
		strings.HasPrefix(branch, RFC3261BranchMagicCookie) &&
		strings.TrimPrefix(branch, RFC3261BranchMagicCookie) != "" {

		isRFC3261 = true
	} else {
		isRFC3261 = false
	}

	var builder strings.Builder
	// RFC 3261 compliant
	if isRFC3261 {
		var port int

		if firstViaHop.Port <= 0 {
			port = int(DefaultPort(firstViaHop.Transport))
		} else {
			port = firstViaHop.Port
		}

		// abuilder.Grow(len(branch) + len(firstViaHop.Host) + len(TxSeperator))
		builder.WriteString(branch)
		builder.WriteString(TxSeperator)
		builder.WriteString(firstViaHop.Host)
		builder.WriteString(TxSeperator)
		builder.WriteString(strconv.Itoa(port))
		builder.WriteString(TxSeperator)
		builder.WriteString(string(method))

		return builder.String(), nil
	}
	// RFC 2543 compliant
	from := msg.From()
	if from == nil {
		return "", fmt.Errorf("'From' header not found in message '%s'", MessageShortString(msg))
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of message '%s'", MessageShortString(msg))
	}
	callId := msg.CallID()
	if callId == nil {
		return "", fmt.Errorf("'Call-ID' header not found in message '%s'", MessageShortString(msg))
	}

	builder.WriteString(fromTag)
	builder.WriteString(TxSeperator)
	callId.StringWrite(&builder)
	builder.WriteString(TxSeperator)
	builder.WriteString(string(method))
	builder.WriteString(TxSeperator)
	builder.WriteString(strconv.Itoa(int(cseq.SeqNo)))
	builder.WriteString(TxSeperator)
	firstViaHop.StringWrite(&builder)
	builder.WriteString(TxSeperator)

	return builder.String(), nil
}

// MakeClientTxKey creates client key for matching responses - RFC 3261 17.1.3.
func MakeClientTxKey(msg Message) (string, error) {
	cseq := msg.CSeq()
	if cseq == nil {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", MessageShortString(msg))
	}
	method := cseq.MethodName
	if method == ACK || method == CANCEL {
		method = INVITE
	}

	firstViaHop := msg.Via()
	if firstViaHop == nil {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", MessageShortString(msg))
	}

	branch, ok := firstViaHop.Params.Get("branch")
	if !ok || len(branch) == 0 ||
		!strings.HasPrefix(branch, RFC3261BranchMagicCookie) ||
		len(strings.TrimPrefix(branch, RFC3261BranchMagicCookie)) == 0 {
		return "", fmt.Errorf("'branch' not found or empty in 'Via' header of message '%s'", MessageShortString(msg))
	}

	var builder strings.Builder
	builder.Grow(len(branch) + len(method) + len(TxSeperator))
	builder.WriteString(branch)
	builder.WriteString(TxSeperator)
	builder.WriteString(string(method))
	return builder.String(), nil
}

type transactionStore struct {
	transactions map[string]Transaction
	mu           sync.RWMutex
}

func newTransactionStore() *transactionStore {
	return &transactionStore{
		transactions: make(map[string]Transaction),
	}
}

func (store *transactionStore) put(key string, tx Transaction) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transactions[key] = tx
}

func (store *transactionStore) get(key string) (Transaction, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	tx, ok := store.transactions[key]
	return tx, ok
}

func (store *transactionStore) drop(key string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, exists := store.transactions[key]
	delete(store.transactions, key)
	return exists
}

func (store *transactionStore) terminateAll() {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, tx := range store.transactions {
		store.mu.RUnlock()
		tx.Terminate() // Calls on terminate to be deleted from store. It is deadlock if called inside loop
		store.mu.RLock()
	}
}
