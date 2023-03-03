package sipgo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"

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

	log zerolog.Logger

	requestMiddlewares  []func(r *sip.Request)
	responseMiddlewares []func(r *sip.Response)
}

type ServerOption func(s *Server) error

// WithServerLogger allows customizing server logger
func WithServerLogger(logger zerolog.Logger) ServerOption {
	return func(s *Server) error {
		s.log = logger
		return nil
	}
}

// NewServer creates new instance of SIP server handle.
// Allows creating server transaction handlers
// It uses User Agent transport and transaction layer
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
		log:                 log.Logger.With().Str("caller", "Server").Logger(),
	}
	for _, o := range options {
		if err := o(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Serve will fire all listeners. Ctx allows canceling
func (srv *Server) ListenAndServe(ctx context.Context, network string, addr string) error {
	return srv.tp.ListenAndServe(ctx, network, addr)
}

// Serve will fire all listeners that are secured. Ctx allows canceling
func (srv *Server) ListenAndServeTLS(ctx context.Context, network string, addr string, conf *tls.Config) error {
	return srv.tp.ListenAndServeTLS(ctx, network, addr, conf)
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

	handler := srv.getHandler(req.Method)

	if handler == nil {
		srv.log.Warn().Msg("SIP request handler not found")
		res := sip.NewResponseFromRequest(req, 405, "Method Not Allowed", nil)
		if err := srv.WriteResponse(res); err != nil {
			srv.log.Error().Msgf("respond '405 Method Not Allowed' failed: %s", err)
		}

		if tx != nil {
			tx.Terminate()
		}
		return
		// for {
		// 	select {
		// 	case <-tx.Done():
		// 		return
		// 	case err, ok := <-tx.Errors():
		// 		if !ok {
		// 			return
		// 		}
		// 		srv.log.Warn().Msgf("error from SIP server transaction %s: %s", tx, err)
		// 	}
		// }
	}

	handler(req, tx)
	if tx != nil {
		// Must be called to prevent any transaction leaks
		tx.Terminate()
	}
}

// WriteResponse will proxy message to transport layer. Use it in stateless mode
func (srv *Server) WriteResponse(r *sip.Response) error {
	return srv.tp.WriteMsg(r)
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

// GenerateTLSConfig creates basic tls.Config that you can pass for ServerTLS
// It needs rootPems for client side
func GenerateTLSConfig(certFile string, keyFile string, rootPems []byte) (*tls.Config, error) {
	roots := x509.NewCertPool()
	if rootPems != nil {
		ok := roots.AppendCertsFromPEM(rootPems)
		if !ok {
			return nil, fmt.Errorf("failed to parse root certificate")
		}
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("fail to load cert. err=%w", err)
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
	}

	return conf, nil
}
