package sipgo

import (
	"context"

	"github.com/emiraganov/sipgo/sip"
	"github.com/emiraganov/sipgo/transport"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// RequestHandler is a callback that will be called on the incoming request
type RequestHandler func(req *sip.Request, tx sip.ServerTransaction)

// Server is a SIP server
type Server struct {
	*UserAgent

	// requestHandlers map of all registered request handlers
	requestHandlers map[sip.RequestMethod]RequestHandler
	listeners       map[string]string //addr:network

	log zerolog.Logger

	requestMiddlewares  []func(r *sip.Request)
	responseMiddlewares []func(r *sip.Response)

	// Default server behavior for sending request in preflight
	RemoveViaHeader bool
}

type ServerOption func(s *Server) error

func WithLogger(logger zerolog.Logger) ServerOption {
	return func(s *Server) error {
		s.log = logger
		return nil
	}
}

// NewServer creates new instance of SIP server.
func NewServer(ua *UserAgent, options ...ServerOption) (*Server, error) {
	s, err := newBaseServer(ua, options...)
	if err != nil {
		return nil, err
	}

	// Handle our transaction layer requests
	s.tx.OnRequest(s.onRequest)
	return s, nil
}

func newBaseServer(ua *UserAgent, options ...ServerOption) (*Server, error) {
	s := &Server{
		UserAgent: ua,
		// userAgent:           "SIPGO",
		// dnsResolver:         net.DefaultResolver,
		requestMiddlewares:  make([]func(r *sip.Request), 0),
		responseMiddlewares: make([]func(r *sip.Response), 0),
		requestHandlers:     make(map[sip.RequestMethod]RequestHandler),
		listeners:           make(map[string]string),
		log:                 log.Logger.With().Str("caller", "Server").Logger(),
	}
	for _, o := range options {
		if err := o(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Listen adds listener for serve
func (srv *Server) Listen(network string, addr string) {
	srv.listeners[addr] = network
}

// Serve will fire all listeners
func (srv *Server) Serve() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return srv.ServeWithContext(ctx)
}

// Serve will fire all listeners. Ctx allows canceling
func (srv *Server) ServeWithContext(ctx context.Context) error {
	defer srv.shutdown()
	for addr, network := range srv.listeners {
		go srv.tp.Serve(ctx, network, addr)
	}
	<-ctx.Done()
	return ctx.Err()
}

// onRequest gets request from Transaction layer
func (srv *Server) onRequest(req *sip.Request, tx sip.ServerTransaction) {
	go srv.handleRequest(req, tx)
}

// handleRequest must be run in seperate goroutine
func (srv *Server) handleRequest(req *sip.Request, tx sip.ServerTransaction) {
	for _, mid := range srv.requestMiddlewares {
		mid(req)
	}

	handler := srv.getHandler(req.Method())

	if handler == nil {
		srv.log.Warn().Msg("SIP request handler not found")
		res := sip.NewResponseFromRequest(req, 405, "Method Not Allowed", nil)
		if err := srv.WriteResponse(res); err != nil {
			srv.log.Error().Msgf("respond '405 Method Not Allowed' failed: %s", err)
		}

		for {
			select {
			case <-tx.Done():
				return
			case err, ok := <-tx.Errors():
				if !ok {
					return
				}
				srv.log.Warn().Msgf("error from SIP server transaction %s: %s", tx, err)
			}
		}
	}

	handler(req, tx)
	if tx != nil {
		// Must be called to prevent any transaction leaks
		tx.Terminate()
	}
}

// TransactionReply is wrapper for calling tx.Respond
// it handles removing Via header by default
func (srv *Server) TransactionReply(tx sip.ServerTransaction, res *sip.Response) error {
	srv.updateResponse(res)
	return tx.Respond(res)
}

// WriteResponse will proxy message to transport layer. Use it in stateless mode
func (srv *Server) WriteResponse(r *sip.Response) error {
	return srv.tp.WriteMsg(r)
}

func (srv *Server) updateResponse(r *sip.Response) {
	if srv.RemoveViaHeader {
		srv.RemoveVia(r)
	}
}

// RemoveVia can be used in case of sending response.
func (srv *Server) RemoveVia(r *sip.Response) {
	if via, exists := r.Via(); exists {
		if via.Host == srv.host {
			// In case it is multipart Via remove only one
			if via.Next != nil {
				via.Remove()
			} else {
				r.RemoveHeader("Via")
			}
		}
	}
}

// Shutdown gracefully shutdowns SIP server
func (srv *Server) shutdown() {
	// stop transaction layer
	srv.tx.Close()
	// stop transport layer
	srv.tp.Close()
}

// OnRequest registers new request callback. Can be used as generic way to add handler
func (srv *Server) OnRequest(method sip.RequestMethod, handler RequestHandler) {
	srv.requestHandlers[method] = handler
}

// OnInvite registers Invite request handler
func (srv *Server) OnInvite(handler RequestHandler) {
	srv.requestHandlers[sip.INVITE] = handler
}

// OnAck registers Ack request handler
func (srv *Server) OnAck(handler RequestHandler) {
	srv.requestHandlers[sip.ACK] = handler
}

// OnCancel registers Cancel request handler
func (srv *Server) OnCancel(handler RequestHandler) {
	srv.requestHandlers[sip.CANCEL] = handler
}

// OnBye registers Bye request handler
func (srv *Server) OnBye(handler RequestHandler) {
	srv.requestHandlers[sip.BYE] = handler
}

// OnRegister registers Register request handler
func (srv *Server) OnRegister(handler RequestHandler) {
	srv.requestHandlers[sip.REGISTER] = handler
}

// OnOptions registers Options request handler
func (srv *Server) OnOptions(handler RequestHandler) {
	srv.requestHandlers[sip.OPTIONS] = handler
}

// OnSubscribe registers Subscribe request handler
func (srv *Server) OnSubscribe(handler RequestHandler) {
	srv.requestHandlers[sip.SUBSCRIBE] = handler
}

// OnNotify registers Notify request handler
func (srv *Server) OnNotify(handler RequestHandler) {
	srv.requestHandlers[sip.NOTIFY] = handler
}

// OnRefer registers Refer request handler
func (srv *Server) OnRefer(handler RequestHandler) {
	srv.requestHandlers[sip.REFER] = handler
}

// OnInfo registers Info request handler
func (srv *Server) OnInfo(handler RequestHandler) {
	srv.requestHandlers[sip.INFO] = handler
}

// OnMessage registers Message request handler
func (srv *Server) OnMessage(handler RequestHandler) {
	srv.requestHandlers[sip.MESSAGE] = handler
}

// OnPrack registers Prack request handler
func (srv *Server) OnPrack(handler RequestHandler) {
	srv.requestHandlers[sip.PRACK] = handler
}

// OnUpdate registers Update request handler
func (srv *Server) OnUpdate(handler RequestHandler) {
	srv.requestHandlers[sip.UPDATE] = handler
}

// OnPublish registers Publish request handler
func (srv *Server) OnPublish(handler RequestHandler) {
	srv.requestHandlers[sip.PUBLISH] = handler
}

func (srv *Server) getHandler(method sip.RequestMethod) (handler RequestHandler) {
	handler, ok := srv.requestHandlers[method]
	if !ok {
		return nil
	}
	return handler
}

// ServeRequest can be used as middleware for preprocessing message
func (srv *Server) ServeRequest(f func(r *sip.Request)) {
	srv.requestMiddlewares = append(srv.requestMiddlewares, f)
}

// TODO can this handled better?
func (srv *Server) ServeResponse(f func(m *sip.Response)) {
	srv.responseMiddlewares = append(srv.responseMiddlewares, f)
	if len(srv.responseMiddlewares) == 1 {
		//Register this only once. TODO CAN THIS
		srv.tp.OnMessage(srv.onTransportMessage)
	}
}

func (srv *Server) onTransportMessage(m sip.Message) {
	//Register transport middleware
	// this avoids allocations and it forces devs to avoid sip.Message usage
	switch r := m.(type) {
	case *sip.Response:
		for _, mid := range srv.responseMiddlewares {
			mid(r)
		}
	}
}

// Transport is function to get transport layer of server
// Can be used for modifying
func (srv *Server) TransportLayer() *transport.Layer {
	return srv.tp
}

// func (srv *Server) OnDialog(f func(d Dialog)) {
// 	dialogs := make(map[string]Dialog)

// 	srv.responseMiddlewares = append(srv.responseMiddlewares, func(r *sip.Response) {
// 		if r.IsInvite() {

// 		}

// 		sip.MakeDialogIDFromMessage()
// 	})

// 	srv.requestMiddlewares = append(srv.requestMiddlewares, func(r *sip.Request) {
// 		if r.IsInvite() {

// 		}

// 		sip.MakeDialogIDFromMessage()
// 	})

// }
