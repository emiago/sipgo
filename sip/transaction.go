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

const (
	T1        = 500 * time.Millisecond
	T2        = 4 * time.Second
	T4        = 5 * time.Second
	Timer_A   = T1
	Timer_B   = 64 * T1
	Timer_D   = 32 * time.Second
	Timer_E   = T1
	Timer_F   = 64 * T1
	Timer_G   = T1
	Timer_H   = 64 * T1
	Timer_I   = T4
	Timer_J   = 64 * T1
	Timer_K   = T4
	Timer_1xx = 200 * time.Millisecond
	Timer_L   = 64 * T1
	Timer_M   = 64 * T1

	TxSeperator = "__"
)

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
	Cancels() <-chan *Request
}

type ClientTransaction interface {
	Transaction
	// Responses returns channel with all responses for transaction
	Responses() <-chan *Response
	// Cancel sends cancel request
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
	fsmState FsmContextState

	log         zerolog.Logger
	onTerminate FnTxTerminate
}

func (tx *commonTx) String() string {
	if tx == nil {
		return "<nil>"
	}

	// fields := tx.Log().Fields().WithFields(log.Fields{
	// 	"key": tx.key,
	// })
	return tx.key

	// return fmt.Sprintf("%s<%s>", tx.Log().Prefix(), fields)
}

func (tx *commonTx) Origin() *Request {
	return tx.origin
}

func (tx *commonTx) Key() string {
	return tx.key
}

// func (tx *commonTx) Transport() Transport {
// 	return tx.tpl
// }

func (tx *commonTx) Done() <-chan struct{} {
	return tx.done
}

func (tx *commonTx) OnTerminate(f FnTxTerminate) {
	tx.onTerminate = f
}

// Choose the right FSM init function depending on request method.
func (tx *commonTx) spinFsm(in FsmInput) {
	tx.fsmMu.Lock()
	for i := in; i != FsmInputNone; {
		i = tx.fsmState(i)
	}
	tx.fsmMu.Unlock()
}

type FnTxTerminate func(key string)

// MakeServerTxKey creates server key for matching retransmitting requests - RFC 3261 17.2.3.
func MakeServerTxKey(msg Message) (string, error) {
	firstViaHop, ok := msg.Via()
	if !ok {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", MessageShortString(msg))
	}

	cseq, ok := msg.CSeq()
	if !ok {
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
	from, ok := msg.From()
	if !ok {
		return "", fmt.Errorf("'From' header not found in message '%s'", MessageShortString(msg))
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of message '%s'", MessageShortString(msg))
	}
	callId, ok := msg.CallID()
	if !ok {
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
	cseq, ok := msg.CSeq()
	if !ok {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", MessageShortString(msg))
	}
	method := cseq.MethodName
	if method == ACK || method == CANCEL {
		method = INVITE
	}

	firstViaHop, ok := msg.Via()
	if !ok {
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
