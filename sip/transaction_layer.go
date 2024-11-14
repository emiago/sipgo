package sip

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type RequestHandler func(req *Request, tx ServerTransaction)
type UnhandledResponseHandler func(req *Response)
type ErrorHandler func(err error)

func defaultRequestHandler(r *Request, tx ServerTransaction) {
	log.Info().Str("caller", "transactionLayer").Str("msg", r.Short()).Msg("Unhandled sip request. OnRequest handler not added")
}

func defaultUnhandledRespHandler(r *Response) {
	log.Info().Str("caller", "transactionLayer").Str("msg", r.Short()).Msg("Unhandled sip response. UnhandledResponseHandler handler not added")
}

type TransactionLayer struct {
	tpl           *TransportLayer
	reqHandler    RequestHandler
	unRespHandler UnhandledResponseHandler

	clientTransactions *transactionStore
	serverTransactions *transactionStore

	log zerolog.Logger
}

func NewTransactionLayer(tpl *TransportLayer) *TransactionLayer {
	txl := &TransactionLayer{
		tpl:                tpl,
		clientTransactions: newTransactionStore(),
		serverTransactions: newTransactionStore(),

		reqHandler:    defaultRequestHandler,
		unRespHandler: defaultUnhandledRespHandler,
	}
	txl.log = log.Logger.With().Str("caller", "transactionLayer").Logger()
	//Send all transport messages to our transaction layer
	tpl.OnMessage(txl.handleMessage)
	return txl
}

func (txl *TransactionLayer) OnRequest(h RequestHandler) {
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
		go txl.handleRequest(msg)
	case *Response:
		go txl.handleResponse(msg)
	default:
		txl.log.Error().Msg("unsupported message, skip it")
	}
}

func (txl *TransactionLayer) handleRequest(req *Request) {
	if req.IsCancel() {
		// Match transaction https://datatracker.ietf.org/doc/html/rfc3261#section-9.2
		// 	The CANCEL method requests that the TU at the server side cancel a
		//    pending transaction.  The TU determines the transaction to be
		//    cancelled by taking the CANCEL request, and then assuming that the
		//    request method is anything but CANCEL or AC

		// For now we only match INVITE
		key, err := makeServerTxKey(req, INVITE)
		if err != nil {
			txl.log.Error().Err(err).Msg("Server tx make key failed")
			return
		}

		tx, exists := txl.getServerTx(key)
		if exists {
			// If ok this should terminate this transaction
			if err := tx.Receive(req); err != nil {
				txl.log.Error().Err(err).Msg("Server tx failed to receive req")
				return
			}

			// Reuse connection and send 200 for CANCEL
			if err := tx.conn.WriteMsg(NewResponseFromRequest(req, StatusOK, "OK", nil)); err != nil {
				txl.log.Error().Err(err).Msg("Failed to respond 200 for CANCEL")
			}
			return
		}
		// Now proceed as normal transaction, and let developer decide what todo with this CANCEL
	}

	key, err := makeServerTxKey(req, "")
	if err != nil {
		txl.log.Error().Err(err).Msg("Server tx make key failed")
		return
	}

	tx, exists := txl.getServerTx(key)
	if exists {
		if err := tx.Receive(req); err != nil {
			txl.log.Error().Err(err).Msg("Server tx failed to receive req")
		}
		return
	}

	// Connection must exist by transport layer.
	// TODO: What if we are gettinb BYE and client closed connection
	conn, err := txl.tpl.GetConnection(req.Transport(), req.Source())
	if err != nil {
		txl.log.Error().Err(err).Msg("Server tx get connection failed")
		return
	}

	tx = NewServerTx(key, req, conn, txl.log)

	if err := tx.Init(); err != nil {
		txl.log.Error().Err(err).Msg("Server tx init failed")
		return
	}
	// put tx to store, to match retransmitting requests later
	txl.serverTransactions.put(tx.Key(), tx)
	tx.OnTerminate(txl.serverTxTerminate)

	txl.reqHandler(req, tx)
}

func (txl *TransactionLayer) handleResponse(res *Response) {
	key, err := MakeClientTxKey(res)
	if err != nil {
		txl.log.Error().Err(err).Msg("Client tx make key failed")
		return
	}

	tx, exists := txl.getClientTx(key)
	if !exists {
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		txl.unRespHandler(res)
		return
	}

	tx.Receive(res)
}

func (txl *TransactionLayer) Request(ctx context.Context, req *Request) (*ClientTx, error) {
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

	conn, err := txl.tpl.ClientRequestConnection(ctx, req)
	if err != nil {
		return nil, err
	}

	// TODO remove this check
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	// TODO
	tx := NewClientTx(key, req, conn, txl.log)

	// Avoid allocations of anonymous functions
	tx.OnTerminate(txl.clientTxTerminate)
	txl.clientTransactions.put(tx.Key(), tx)

	if err := tx.Init(); err != nil {
		txl.clientTxTerminate(tx.key) //Force termination here
		return nil, err
	}

	return tx, nil
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

func (txl *TransactionLayer) clientTxTerminate(key string) {
	if !txl.clientTransactions.drop(key) {
		txl.log.Info().Str("key", key).Msg("Non existing client tx was removed")
	}
}

func (txl *TransactionLayer) serverTxTerminate(key string) {
	if !txl.serverTransactions.drop(key) {
		txl.log.Info().Str("key", key).Msg("Non existing server tx was removed")
	}
}

// RFC 17.1.3.
func (txl *TransactionLayer) getClientTx(key string) (*ClientTx, bool) {
	tx, ok := txl.clientTransactions.get(key)
	if !ok {
		return nil, false
	}
	return tx.(*ClientTx), true
}

// RFC 17.2.3.
func (txl *TransactionLayer) getServerTx(key string) (*ServerTx, bool) {
	tx, ok := txl.serverTransactions.get(key)
	if !ok {
		return nil, false
	}
	return tx.(*ServerTx), true
}

func (txl *TransactionLayer) Close() {
	txl.clientTransactions.terminateAll()
	txl.serverTransactions.terminateAll()
	txl.log.Debug().Msg("transaction layer closed")
}

func (txl *TransactionLayer) Transport() *TransportLayer {
	return txl.tpl
}
