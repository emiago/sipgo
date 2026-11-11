package transaction

import (
	"fmt"
	"log/slog"

	"github.com/livekit/sipgo/sip"
	"github.com/livekit/sipgo/transport"
)

type RequestHandler func(log *slog.Logger, req *sip.Request, tx sip.ServerTransaction)
type UnhandledResponseHandler func(log *slog.Logger, req *sip.Response)
type ErrorHandler func(err error)

func defaultRequestHandler(log *slog.Logger, r *sip.Request, tx sip.ServerTransaction) {
	log.Debug("Unhandled sip request. OnRequest handler not added", "msg", r.Short())
}

func defaultUnhandledRespHandler(log *slog.Logger, r *sip.Response) {
	log.Debug("Unhandled sip response. UnhandledResponseHandler handler not added", "msg", r.Short())
}

type Layer struct {
	log           *slog.Logger
	tpl           *transport.Layer
	reqHandler    RequestHandler
	unRespHandler UnhandledResponseHandler

	clientTransactions *transactionStore
	serverTransactions *transactionStore
}

func NewLayer(log *slog.Logger, tpl *transport.Layer) *Layer {
	txl := &Layer{
		log:                log.With("caller", "transaction.Layer"),
		tpl:                tpl,
		clientTransactions: newTransactionStore(),
		serverTransactions: newTransactionStore(),

		reqHandler:    defaultRequestHandler,
		unRespHandler: defaultUnhandledRespHandler,
	}
	//Send all transport messages to our transaction layer
	tpl.OnMessage(txl.handleMessage)
	return txl
}

func (txl *Layer) OnRequest(h RequestHandler) {
	txl.reqHandler = h
}

// UnhandledResponseHandler can be used in case missing client transactions for handling response
// ServerTransaction handle responses by state machine
func (txl *Layer) UnhandledResponseHandler(f UnhandledResponseHandler) {
	txl.unRespHandler = f
}

// handleMessage is entry for handling requests and responses from transport
func (txl *Layer) handleMessage(msg sip.Message) {
	switch msg := msg.(type) {
	case *sip.Request:
		// TODO Consider making goroutine here already?
		txl.handleRequest(msg)
	case *sip.Response:
		// TODO Consider making goroutine here already?
		txl.handleResponse(msg)
	default:
		txl.log.Error("unsupported message, skip it")
		// todo pass up error?
	}
}

func (txl *Layer) handleRequest(req *sip.Request) {
	// ACK and CANCEL belong to the INVITE transaction when the branch and sent-by match
	// (RFC 3261 §17.2.3, §9.2). Otherwise each is its own transaction, keyed by its own
	// method, and the server hands it to OnAck or OnCancel.
	// CANCEL handling is ported from upstream sipgo v1.6.0. Upstream still keys an
	// unmatched ACK as INVITE; we key it as ACK so it cannot collide with an INVITE tx.
	if req.IsAck() || req.IsCancel() {
		if txl.matchInviteTransaction(req) {
			return
		}
	}

	key, err := makeServerTxKey(req, "")
	if err != nil {
		// Any remote peer can send a request with missing or broken headers,
		// internet scanners in particular, so this is not a server error.
		txl.log.Warn("Server tx make key failed", "err", err)
		return
	}

	tx, exists := txl.getServerTx(key)
	if exists {
		if err := tx.Receive(req); err != nil {
			txl.log.Error("Server tx failed to receive req", "err", err)
		}
		return
	}

	// Connection must exist by transport layer.
	// TODO: What if we are gettinb BYE and client closed connection
	conn, err := txl.tpl.GetConnection(req.Transport(), req.Source())
	if err != nil {
		txl.log.Error("Server tx get connection failed", "err", err)
		return
	}

	tx = NewServerTx(key, req, conn, txl.log)

	if err := tx.Init(); err != nil {
		txl.log.Error("Server tx init failed", "err", err)
		return
	}
	// put tx to store, to match retransmitting requests later
	txl.serverTransactions.put(tx.Key(), tx)
	tx.OnTerminate(txl.serverTxTerminate)

	txl.reqHandler(tx.log, req, tx)
}

// matchInviteTransaction delivers an ACK or CANCEL to its INVITE server transaction.
// It reports whether that transaction exists. A matching CANCEL is answered with 200
// on the INVITE's connection; the INVITE itself moves to 487 from its state machine.
func (txl *Layer) matchInviteTransaction(req *sip.Request) bool {
	key, err := makeServerTxKey(req, sip.INVITE)
	if err != nil {
		txl.log.Warn("Server tx make key failed", "err", err)
		return true
	}
	tx, exists := txl.getServerTx(key)
	if !exists {
		return false
	}
	if req.IsCancel() {
		// 200 before the 487, as upstream does: it stops the UAC's CANCEL
		// retransmissions first.
		res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		if err := tx.conn.WriteMsg(res); err != nil {
			txl.log.Error("send '200 OK' for CANCEL failed", "err", err)
			return true
		}
	}
	if err := tx.Receive(req); err != nil {
		txl.log.Error("Server tx failed to receive req", "err", err)
	}
	return true
}

func (txl *Layer) handleResponse(res *sip.Response) {
	key, err := MakeClientTxKey(res)
	if err != nil {
		txl.log.Error("Client tx make key failed", "err", err)
		return
	}

	tx, exists := txl.getClientTx(key)
	if !exists {
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		txl.unRespHandler(txl.log, res)
		return
	}

	if err := tx.Receive(res); err != nil {
		txl.log.Error("Client tx failed to receive response", "err", err)
		return
	}
}

func (txl *Layer) Request(req *sip.Request) (*ClientTx, error) {
	if req.IsAck() {
		return nil, fmt.Errorf("ACK request must be sent directly through transport")
	}

	key, err := MakeClientTxKey(req)
	if err != nil {
		return nil, err
	}

	if _, exists := txl.clientTransactions.get(key); exists {
		return nil, fmt.Errorf("transaction %q already exists", key)
	}

	conn, err := txl.tpl.ClientRequestConnection(req)
	if err != nil {
		return nil, err
	}

	// TODO remove this check
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	// TODO
	tx := NewClientTx(key, req, conn, txl.log)
	if err != nil {
		return nil, err
	}

	// Avoid allocations of anonymous functions
	tx.OnTerminate(txl.clientTxTerminate)
	txl.clientTransactions.put(tx.Key(), tx)

	if err := tx.Init(); err != nil {
		txl.clientTxTerminate(tx.key) //Force termination here
		return nil, err
	}

	return tx, nil
}

func (txl *Layer) Respond(res *sip.Response) (*ServerTx, error) {
	key, err := MakeServerTxKey(res)
	if err != nil {
		return nil, err
	}

	tx, exists := txl.getServerTx(key)
	if !exists {
		return nil, fmt.Errorf("transaction does not exists")
	}

	err = tx.Respond(res)
	if err != nil {
		return nil, err
	}

	return tx, nil
}

func (txl *Layer) clientTxTerminate(key string) {
	if !txl.clientTransactions.drop(key) {
		txl.log.Info("Non existing client tx was removed", "key", key)
	}
}

func (txl *Layer) serverTxTerminate(key string) {
	if !txl.serverTransactions.drop(key) {
		txl.log.Info("Non existing server tx was removed", "key", key)
	}
}

// RFC 17.1.3.
func (txl *Layer) getClientTx(key string) (*ClientTx, bool) {
	tx, ok := txl.clientTransactions.get(key)
	if !ok {
		return nil, false
	}
	return tx.(*ClientTx), true
}

// RFC 17.2.3.
func (txl *Layer) getServerTx(key string) (*ServerTx, bool) {
	tx, ok := txl.serverTransactions.get(key)
	if !ok {
		return nil, false
	}
	return tx.(*ServerTx), true
}

func (txl *Layer) Close() {
	for _, tx := range txl.clientTransactions.all() {
		tx.Terminate()
	}

	for _, tx := range txl.serverTransactions.all() {
		tx.Terminate()
	}
	txl.log.Debug("transaction layer closed")
}

func (txl *Layer) Transport() sip.Transport {
	return txl.tpl
}
