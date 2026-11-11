// transaction package implements SIP Transaction Layer
package transaction

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/livekit/sipgo/sip"
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
	ErrTimeout   = errors.New("transaction timeout")
	ErrTransport = errors.New("transaction transport error")
)

func wrapTransportError(err error) error {
	return fmt.Errorf("%w. %w", err, ErrTransport)
}

func wrapTimeoutError(err error) error {
	return fmt.Errorf("%s. %w", err.Error(), ErrTimeout)
}

type FnTxTerminate func(key string)

// MakeServerTxKey creates server key for matching retransmitting requests - RFC 3261 17.2.3.
// ACK and CANCEL keep their own method. Match them to an INVITE with makeServerTxKey(msg, sip.INVITE).
func MakeServerTxKey(msg sip.Message) (string, error) {
	return makeServerTxKey(msg, "")
}

// makeServerTxKey is MakeServerTxKey, but asMethod replaces the CSeq method when set.
// ACK and CANCEL are the INVITE's transaction when the branch and sent-by match, and
// their own transaction otherwise, so the caller chooses which method the key uses.
func makeServerTxKey(msg sip.Message, asMethod sip.RequestMethod) (string, error) {
	firstViaHop := msg.Via()
	if firstViaHop == nil {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", sip.MessageShortString(msg))
	}

	cseq := msg.CSeq()
	if cseq == nil {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", sip.MessageShortString(msg))
	}
	method := cseq.MethodName
	if asMethod != "" {
		method = asMethod
	}

	var isRFC3261 bool
	branch, ok := firstViaHop.Params.Get("branch")
	if ok && branch != "" &&
		strings.HasPrefix(branch, sip.RFC3261BranchMagicCookie) &&
		strings.TrimPrefix(branch, sip.RFC3261BranchMagicCookie) != "" {

		isRFC3261 = true
	} else {
		isRFC3261 = false
	}

	var builder strings.Builder
	// RFC 3261 compliant
	if isRFC3261 {
		var port int

		if firstViaHop.Port <= 0 {
			port = int(sip.DefaultPort(firstViaHop.Transport))
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
		return "", fmt.Errorf("'From' header not found in message '%s'", sip.MessageShortString(msg))
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of message '%s'", sip.MessageShortString(msg))
	}
	callId := msg.CallID()
	if callId == nil {
		return "", fmt.Errorf("'Call-ID' header not found in message '%s'", sip.MessageShortString(msg))
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
func MakeClientTxKey(msg sip.Message) (string, error) {
	cseq := msg.CSeq()
	if cseq == nil {
		return "", fmt.Errorf("'CSeq' header not found in message '%s'", sip.MessageShortString(msg))
	}
	method := cseq.MethodName
	if method == sip.ACK || method == sip.CANCEL {
		method = sip.INVITE
	}

	firstViaHop := msg.Via()
	if firstViaHop == nil {
		return "", fmt.Errorf("'Via' header not found or empty in message '%s'", sip.MessageShortString(msg))
	}

	branch, ok := firstViaHop.Params.Get("branch")
	if !ok || len(branch) == 0 ||
		!strings.HasPrefix(branch, sip.RFC3261BranchMagicCookie) ||
		len(strings.TrimPrefix(branch, sip.RFC3261BranchMagicCookie)) == 0 {
		return "", fmt.Errorf("'branch' not found or empty in 'Via' header of message '%s'", sip.MessageShortString(msg))
	}

	var builder strings.Builder
	builder.Grow(len(branch) + len(method) + len(TxSeperator))
	builder.WriteString(branch)
	builder.WriteString(TxSeperator)
	builder.WriteString(string(method))
	return builder.String(), nil
}

type transactionStore struct {
	transactions map[string]sip.Transaction
	mu           sync.RWMutex
}

func newTransactionStore() *transactionStore {
	return &transactionStore{
		transactions: make(map[string]sip.Transaction),
	}
}

func (store *transactionStore) put(key string, tx sip.Transaction) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transactions[key] = tx
}

func (store *transactionStore) get(key string) (sip.Transaction, bool) {
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

func (store *transactionStore) all() []sip.Transaction {
	all := make([]sip.Transaction, 0)
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, tx := range store.transactions {
		all = append(all, tx)
	}

	return all
}
