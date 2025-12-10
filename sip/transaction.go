package sip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
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

	TransactionFSMDebug bool
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
	ErrTransactionTimeout    = errors.New("transaction timeout")
	ErrTransactionTransport  = errors.New("transaction transport error")
	ErrTransactionCanceled   = errors.New("transaction canceled")
	ErrTransactionTerminated = errors.New("transaction terminated")
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

	// OnTerminate can be registered to be called when transaction terminates.
	// It is alternative to tx.Done where you avoid creating more goroutines.
	// It returns false if transaction already terminated.
	// NOTE: calling tx methods inside this func can DEADLOCK
	//
	// Experimental
	OnTerminate(f FnTxTerminate) bool

	// Done when transaction fsm terminates. Can be called multiple times
	Done() <-chan struct{}

	// Err that stopped transaction. Useful to check when transaction terminates
	Err() error
}

type ServerTransaction interface {
	Transaction

	// Respond sends response. It is expected that is prebuilt with correct headers
	// Use NewResponseFromRequest to build response
	Respond(res *Response) error
	// Acks returns ACK during transaction.
	Acks() <-chan *Request

	// OnCancel will be fired when CANCEL request is received
	// It allows you to detect CANCEL request, which will be followed by termination.
	// It returns false in case transaction already terminated
	// NOTE: You must not block here too long. In that case fire go routine.
	//
	// Experimental
	OnCancel(f FnTxCancel) bool
}

// ServerTransactionContext creates server transaction cancelation via context.Context
// This is useful if you want to pass this on underhood APIs
// Should not be called more than once per transaction
func ServerTransactionContext(tx ServerTransaction) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	done := tx.OnTerminate(func(key string, err error) {
		cancel()
	})
	if done {
		cancel()
	}
	return ctx
}

type ClientTransaction interface {
	Transaction
	// Responses returns channel with all responses for transaction
	Responses() <-chan *Response

	// Register response retransmission hook.
	OnRetransmission(f FnTxResponse) bool
}

type baseTx struct {
	mu sync.Mutex

	key    string
	origin *Request

	conn   Connection
	done   chan struct{}
	closed bool

	//State machine control
	fsmMu    sync.Mutex
	fsmState fsmContextState

	// fsmResp fsmErr fsmAck fsmCancel are set on spin FSM
	// Use it only if tx is inside fsm State
	// outside is not thread safe and it must be protected with fsm Lock
	fsmResp   *Response
	fsmErr    error
	fsmAck    *Request
	fsmCancel *Request

	log         *slog.Logger
	onTerminate FnTxTerminate
}

func (tx *baseTx) String() string {
	if tx == nil {
		return "<nil>"
	}

	return tx.key
}

func (tx *baseTx) Origin() *Request {
	return tx.origin
}

func (tx *baseTx) Key() string {
	return tx.key
}

func (tx *baseTx) Done() <-chan struct{} {
	return tx.done
}

// OnTerminate is experimental
// Callback function can not call any fsm related functions as it will cause deadlock like.
// Err must not be called,instead error is passed
func (tx *baseTx) OnTerminate(f FnTxTerminate) bool {
	tx.mu.Lock()
	select {
	case <-tx.done:
		tx.mu.Unlock()
		// Already terminated
		return false
	default:
	}
	defer tx.mu.Unlock()

	if tx.onTerminate != nil {
		prev := tx.onTerminate
		tx.onTerminate = func(key string, err error) {
			prev(key, err)
			f(key, err)
		}
		return true
	}
	tx.onTerminate = f
	return true
}

// TODO
// FSM should be moved out commontx to seperate struct
func (tx *baseTx) currentFsmState() fsmContextState {
	tx.fsmMu.Lock()
	defer tx.fsmMu.Unlock()
	return tx.fsmState
}

// Initialises the correct kind of FSM based on request method.
func (tx *baseTx) initFSM(fsmState fsmContextState) {
	tx.fsmMu.Lock()
	tx.fsmState = fsmState
	tx.fsmMu.Unlock()
}

func (tx *baseTx) spinFsmUnsafe(in fsmInput) {
	for i := in; i != FsmInputNone; {
		if TransactionFSMDebug {
			fname := runtime.FuncForPC(reflect.ValueOf(tx.fsmState).Pointer()).Name()
			fname = fname[strings.LastIndex(fname, ".")+1:]
			tx.log.Debug("Changing transaction state", "key", tx.key, "input", fsmString(i), "state", fname)
		}
		i = tx.fsmState(i)
	}
}

// Choose the right FSM init function depending on request method.
func (tx *baseTx) spinFsm(in fsmInput) {
	tx.fsmMu.Lock()
	tx.spinFsmUnsafe(in)
	tx.fsmMu.Unlock()
}

func (tx *baseTx) spinFsmWithResponse(in fsmInput, resp *Response) {
	tx.fsmMu.Lock()
	tx.fsmResp = resp
	tx.spinFsmUnsafe(in)
	tx.fsmMu.Unlock()
}

func (tx *baseTx) spinFsmWithRequest(in fsmInput, req *Request) {
	// TODO do we really need handling ACK and Cancel seperate
	tx.fsmMu.Lock()
	switch {
	case req.IsAck(): // ACK for non-2xx response
		tx.fsmAck = req
	case req.IsCancel():
		tx.fsmCancel = req
	}
	tx.spinFsmUnsafe(in)
	tx.fsmMu.Unlock()
}

func (tx *baseTx) spinFsmWithError(in fsmInput, err error) {
	tx.fsmMu.Lock()
	tx.fsmErr = err
	tx.spinFsmUnsafe(in)
	tx.fsmMu.Unlock()
}

func (tx *baseTx) Err() error {
	tx.fsmMu.Lock()
	err := tx.fsmErr
	tx.fsmMu.Unlock()
	return err
}

type FnTxTerminate func(key string, err error)
type FnTxCancel func(r *Request)
type FnTxResponse func(r *Response)

func isRFC3261(branch string) bool {
	return branch != "" &&
		strings.HasPrefix(branch, RFC3261BranchMagicCookie) &&
		strings.TrimPrefix(branch, RFC3261BranchMagicCookie) != ""
}

// ServerTxKeyMake creates server key for matching retransmitting requests - RFC 3261 17.2.3.
func ServerTxKeyMake(msg Message) (string, error) {
	return makeServerTxKey(msg, "")
}

// MakeServerTxKey creates server key for matching retransmitting requests - RFC 3261 17.2.3.
// https://datatracker.ietf.org/doc/html/rfc3261#section-17.2.3
func makeServerTxKey(msg Message, asMethod RequestMethod) (string, error) {
	firstViaHop := msg.Via()
	if firstViaHop == nil {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", messageShortString(msg))
	}

	cseq := msg.CSeq()
	if cseq == nil {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", messageShortString(msg))
	}
	method := cseq.MethodName
	if method == ACK {
		method = INVITE
	}

	if asMethod != "" {
		method = asMethod
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
		return "", fmt.Errorf("'From' header not found in message '%s'", messageShortString(msg))
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of message '%s'", messageShortString(msg))
	}
	callId := msg.CallID()
	if callId == nil {
		return "", fmt.Errorf("'Call-ID' header not found in message '%s'", messageShortString(msg))
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

// ClientTxKeyMake creates client key for matching responses - RFC 3261 17.1.3.
func ClientTxKeyMake(msg Message) (string, error) {
	return makeClientTxKey(msg, "")
}

func makeClientTxKey(msg Message, asMethod RequestMethod) (string, error) {
	cseq := msg.CSeq()
	if cseq == nil {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", messageShortString(msg))
	}
	method := cseq.MethodName
	if method == ACK {
		method = INVITE
	}

	if asMethod != "" {
		method = asMethod
	}

	firstViaHop := msg.Via()
	if firstViaHop == nil {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", messageShortString(msg))
	}

	branch, ok := firstViaHop.Params.Get("branch")
	if !ok || len(branch) == 0 ||
		!strings.HasPrefix(branch, RFC3261BranchMagicCookie) ||
		len(strings.TrimPrefix(branch, RFC3261BranchMagicCookie)) == 0 {
		return "", fmt.Errorf("'branch' not found or empty in 'Via' header of message '%s'", messageShortString(msg))
	}

	var builder strings.Builder
	builder.Grow(len(branch) + len(method) + len(TxSeperator))
	builder.WriteString(branch)
	builder.WriteString(TxSeperator)
	builder.WriteString(string(method))
	return builder.String(), nil
}

type transactionStore[T Transaction] struct {
	items map[string]T
	mu    sync.RWMutex
}

func newTransactionStore[T Transaction]() *transactionStore[T] {
	return &transactionStore[T]{
		items: make(map[string]T),
	}
}

func (store *transactionStore[T]) lock() {
	store.mu.Lock()
}

func (store *transactionStore[T]) unlock() {
	store.mu.Unlock()
}

func (store *transactionStore[T]) put(key string, tx T) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.items[key] = tx
}

func (store *transactionStore[T]) get(key string) (T, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	tx, ok := store.items[key]
	return tx, ok
}

func (store *transactionStore[T]) drop(key string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, exists := store.items[key]
	delete(store.items, key)
	return exists
}

func (store *transactionStore[T]) terminateAll() {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, tx := range store.items {
		store.mu.RUnlock()
		tx.Terminate() // Calls on terminate to be deleted from store. It is deadlock if called inside loop
		store.mu.RLock()
	}
}
