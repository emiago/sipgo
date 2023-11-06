package transaction

import (
	"context"
	"fmt"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type RequestHandler func(req *sip.Request, tx sip.ServerTransaction)
type UnhandledResponseHandler func(req *sip.Response)
type ErrorHandler func(err error)

func defaultRequestHandler(r *sip.Request, tx sip.ServerTransaction) {
	log.Info().Str("caller", "transaction.Layer").Str("msg", r.Short()).Msg("Unhandled sip request. OnRequest handler not added")
}

func defaultUnhandledRespHandler(r *sip.Response) {
	log.Info().Str("caller", "transaction.Layer").Str("msg", r.Short()).Msg("Unhandled sip response. UnhandledResponseHandler handler not added")
}

type Layer struct {
	tpl           *transport.Layer
	reqHandler    RequestHandler
	unRespHandler UnhandledResponseHandler

	clientTransactions *transactionStore
	serverTransactions *transactionStore

	log zerolog.Logger
}

func NewLayer(tpl *transport.Layer) *Layer {
	txl := &Layer{
		tpl:                tpl,
		clientTransactions: newTransactionStore(),
		serverTransactions: newTransactionStore(),

		reqHandler:    defaultRequestHandler,
		unRespHandler: defaultUnhandledRespHandler,
	}
	txl.log = log.Logger.With().Str("caller", "transaction.Layer").Logger()
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
		txl.log.Error().Msg("unsupported message, skip it")
		// todo pass up error?
	}
}

func (txl *Layer) handleRequest(req *sip.Request) {
	key, err := MakeServerTxKey(req)
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

	if req.IsCancel() {
		// transaction for CANCEL already completed and terminated
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

func (txl *Layer) handleResponse(res *sip.Response) {
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

	if err := tx.Receive(res); err != nil {
		txl.log.Error().Err(err).Msg("Client tx failed to receive response")
		return
	}
}

func (txl *Layer) Request(ctx context.Context, req *sip.Request) (*ClientTx, error) {
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
		txl.log.Info().Str("key", key).Msg("Non existing client tx was removed")
	}
}

func (txl *Layer) serverTxTerminate(key string) {
	if !txl.serverTransactions.drop(key) {
		txl.log.Info().Str("key", key).Msg("Non existing server tx was removed")
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
	txl.clientTransactions.terminateAll()
	txl.serverTransactions.terminateAll()
	txl.log.Debug().Msg("transaction layer closed")
}

func (txl *Layer) Transport() sip.Transport {
	return txl.tpl
}
