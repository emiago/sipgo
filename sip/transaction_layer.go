package sip

import (
	"context"
	"fmt"
	"log/slog"
)

type TransactionRequestHandler func(req *Request, tx *ServerTx)
type UnhandledResponseHandler func(req *Response)
type ErrorHandler func(err error)

func defaultRequestHandler(r *Request, tx *ServerTx) {
	slog.Info("Unhandled sip request. OnRequest handler not added", "caller", "transactionLayer", "msg", r.Short())
}

func defaultUnhandledRespHandler(r *Response) {
	slog.Info("Unhandled sip response. UnhandledResponseHandler handler not added", "caller", "transactionLayer", "msg", r.Short())
}

type TransactionLayer struct {
	tpl           *TransportLayer
	reqHandler    TransactionRequestHandler
	unRespHandler UnhandledResponseHandler

	clientTransactions *transactionStore[*ClientTx]
	serverTransactions *transactionStore[*ServerTx]

	log *slog.Logger
}

type TransactionLayerOption func(tpl *TransactionLayer)

func WithTransactionLayerLogger(l *slog.Logger) TransactionLayerOption {
	return func(txl *TransactionLayer) {
		if l != nil {
			txl.log = l.With("caller", "TransactionLayer")
		}
	}
}

func NewTransactionLayer(tpl *TransportLayer, options ...TransactionLayerOption) *TransactionLayer {
	txl := &TransactionLayer{
		tpl:                tpl,
		clientTransactions: newTransactionStore[*ClientTx](),
		serverTransactions: newTransactionStore[*ServerTx](),

		reqHandler:    defaultRequestHandler,
		unRespHandler: defaultUnhandledRespHandler,
	}
	txl.log = slog.With("caller", "TransactionLayer")

	for _, o := range options {
		o(txl)
	}

	//Send all transport messages to our transaction layer
	tpl.OnMessage(txl.handleMessage)
	return txl
}

func (txl *TransactionLayer) OnRequest(h TransactionRequestHandler) {
	txl.reqHandler = h
}

// UnhandledResponseHandler can be used in case missing client transactions for handling response
// ServerTransaction handle responses by state machine
func (txl *TransactionLayer) UnhandledResponseHandler(f UnhandledResponseHandler) {
	txl.unRespHandler = f
}

// handleMessage is entry for handling requests and responses from transport
func (txl *TransactionLayer) handleMessage(msg Message) {
	// Having concurency here we increased throghput but also solving deadlock
	// Current client transactions are blocking on passUp and this may block when calling tx.Receive
	// forking here can remove this

	switch msg := msg.(type) {
	case *Request:
		go txl.handleRequestBackground(msg)
	case *Response:
		go txl.handleResponseBackground(msg)
	default:
		txl.log.Error("unsupported message, skip it")
	}
}

func (txl *TransactionLayer) handleRequestBackground(req *Request) {
	if err := txl.handleRequest(req); err != nil {
		txl.log.Error("Server tx failed to handle request", "error", err, "req", req.StartLine())
	}
}

func (txl *TransactionLayer) handleRequest(req *Request) error {
	if req.IsCancel() {
		// Match transaction https://datatracker.ietf.org/doc/html/rfc3261#section-9.2
		// 	The CANCEL method requests that the TU at the server side cancel a
		//    pending transaction.  The TU determines the transaction to be
		//    cancelled by taking the CANCEL request, and then assuming that the
		//    request method is anything but CANCEL or AC

		// For now we only match INVITE
		key, err := makeServerTxKey(req, INVITE)
		if err != nil {
			return fmt.Errorf("make key failed: %w", err)
		}

		tx, exists := txl.getServerTx(key)
		if exists {
			// If ok this should terminate this transaction
			if err := tx.Receive(req); err != nil {
				return fmt.Errorf("failed to receive req: %w", err)
			}

			// Reuse connection and send 200 for CANCEL
			if err := tx.conn.WriteMsg(NewResponseFromRequest(req, StatusOK, "OK", nil)); err != nil {
				return fmt.Errorf("Failed to respond 200 for CANCEL: %w", err)
			}
			return nil
		}
		// Now proceed as normal transaction, and let developer decide what todo with this CANCEL
	}

	key, err := makeServerTxKey(req, "")
	if err != nil {
		return fmt.Errorf("make key failed: %w", err)
	}

	return txl.serverTxRequest(req, key)
}

func (txl *TransactionLayer) serverTxRequest(req *Request, key string) error {
	txl.serverTransactions.lock()
	tx, exists := txl.serverTransactions.items[key]
	if exists {
		txl.serverTransactions.unlock()
		if err := tx.Receive(req); err != nil {
			return fmt.Errorf("failed to receive req: %w", err)
		}
		return nil
	}

	tx, err := txl.serverTxCreate(req, key)
	if err != nil {
		txl.serverTransactions.unlock()
		return err
	}

	// put tx to store
	txl.serverTransactions.items[key] = tx
	tx.OnTerminate(txl.serverTxTerminate)
	txl.serverTransactions.unlock()

	// pass request and transaction to handler
	txl.reqHandler(req, tx)
	return nil
}

func (txl *TransactionLayer) serverTxCreate(req *Request, key string) (*ServerTx, error) {
	// Connection must exist by transport layer.
	// TODO: What if we are getting BYE and client closed connection
	conn, err := txl.tpl.GetConnection(req.Transport(), req.Source())
	if err != nil {
		return nil, fmt.Errorf("server tx get connection failed: %w", err)
	}

	tx := NewServerTx(key, req, conn, txl.log)
	return tx, tx.Init()
}

func (txl *TransactionLayer) handleResponseBackground(res *Response) {
	if err := txl.handleResponse(res); err != nil {
		txl.log.Error("Client tx failed to handle response", "error", err)
	}
}

func (txl *TransactionLayer) handleResponse(res *Response) error {
	key, err := MakeClientTxKey(res)
	if err != nil {
		return fmt.Errorf("make key failed: %w", err)
	}

	tx, exists := txl.getClientTx(key)
	if !exists {
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		txl.unRespHandler(res)
		return nil
	}

	tx.Receive(res)
	return nil
}

func (txl *TransactionLayer) Request(ctx context.Context, req *Request) (*ClientTx, error) {
	if req.IsAck() {
		return nil, fmt.Errorf("ACK request must be sent directly through transport")
	}

	key, err := MakeClientTxKey(req)
	if err != nil {
		return nil, err
	}

	return txl.clientTxRequest(ctx, req, key)
}

func (txl *TransactionLayer) clientTxRequest(ctx context.Context, req *Request, key string) (*ClientTx, error) {
	txl.clientTransactions.lock()
	tx, exists := txl.clientTransactions.items[key]
	if exists {
		txl.clientTransactions.unlock()
		return nil, fmt.Errorf("client transaction %q already exists", key)
	}

	tx, err := txl.clientTxCreate(ctx, req, key)
	if err != nil {
		txl.clientTransactions.unlock()
		return nil, fmt.Errorf("failed to create client transaction: %w", err)
	}

	txl.clientTransactions.items[key] = tx
	tx.OnTerminate(txl.clientTxTerminate)
	txl.clientTransactions.unlock()
	return tx, nil
}

func (txl *TransactionLayer) clientTxCreate(ctx context.Context, req *Request, key string) (*ClientTx, error) {
	conn, err := txl.tpl.ClientRequestConnection(ctx, req)
	if err != nil {
		return nil, err
	}

	tx := NewClientTx(key, req, conn, txl.log)
	return tx, tx.Init()
}

func (txl *TransactionLayer) Respond(res *Response) (*ServerTx, error) {
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

func (txl *TransactionLayer) clientTxTerminate(key string, err error) {
	if !txl.clientTransactions.drop(key) {
		txl.log.Info("Non existing client tx was removed", "tx", key)
	}
}

func (txl *TransactionLayer) serverTxTerminate(key string, err error) {
	if !txl.serverTransactions.drop(key) {
		txl.log.Info("Non existing server tx was removed", "tx", key)
	}
}

// RFC 17.1.3.
func (txl *TransactionLayer) getClientTx(key string) (*ClientTx, bool) {
	return txl.clientTransactions.get(key)
	// tx, ok := txl.clientTransactions.get(key)
	// if !ok {
	// 	return nil, false
	// }
	// return tx.(*ClientTx), true
}

// RFC 17.2.3.
func (txl *TransactionLayer) getServerTx(key string) (*ServerTx, bool) {
	return txl.serverTransactions.get(key)
	// tx, ok := txl.serverTransactions.get(key)
	// if !ok {
	// 	return nil, false
	// }
	// return tx.(*ServerTx), true
}

func (txl *TransactionLayer) Close() {
	txl.clientTransactions.terminateAll()
	txl.serverTransactions.terminateAll()
	txl.log.Debug("transaction layer closed")
}

func (txl *TransactionLayer) Transport() *TransportLayer {
	return txl.tpl
}
