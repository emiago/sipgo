package sipgo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"strings"

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
	noRouteHandler  RequestHandler

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

	// TODO have this exported as option
	s.noRouteHandler = s.defaultUnhandledHandler

	return s, nil
}

var (
	// Used only for testing, better way is to pass listener with Serve{Transport}
	ctxTestListenAndServeReady = "ctxTestListenAndServeReady"
)

// Serve will fire all listeners
// Network supported: udp, tcp, ws
func (srv *Server) ListenAndServe(ctx context.Context, network string, addr string) error {
	network = strings.ToLower(network)
	var connCloser io.Closer

	// TODO consider different design to avoid this additional go routines
	go func() {
		select {
		case <-ctx.Done():
			if connCloser == nil {
				return
			}
			if err := connCloser.Close(); err != nil {
				srv.log.Error().Err(err).Msg("Failed to close listener")
			}

		}
	}()

	switch network {
	case "udp":
		// resolve local UDP endpoint
		laddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}
		udpConn, err := net.ListenUDP("udp", laddr)
		if err != nil {
			return fmt.Errorf("listen udp error. err=%w", err)
		}

		connCloser = udpConn
		if v := ctx.Value(ctxTestListenAndServeReady); v != nil {
			close(v.(chan any))
		}
		return srv.tp.ServeUDP(udpConn)

	case "ws", "tcp":
		laddr, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		conn, err := net.ListenTCP("tcp", laddr)
		if err != nil {
			return fmt.Errorf("listen tcp error. err=%w", err)
		}

		connCloser = conn
		if v := ctx.Value(ctxTestListenAndServeReady); v != nil {
			close(v.(chan any))
		}
		// and uses listener to buffer
		if network == "ws" {
			return srv.tp.ServeWS(conn)
		}

		return srv.tp.ServeTCP(conn)
	}
	return transport.ErrNetworkNotSuported
}

// Serve will fire all listeners that are secured.
// Network supported: tls, wss
func (srv *Server) ListenAndServeTLS(ctx context.Context, network string, addr string, conf *tls.Config) error {
	network = strings.ToLower(network)

	var connCloser io.Closer
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// TODO consider different design to avoid this additional go routines
	go func() {
		select {
		case <-ctx.Done():
			if connCloser == nil {
				return
			}
			if err := connCloser.Close(); err != nil {
				srv.log.Error().Err(err).Msg("Failed to close listener")
			}

		}
	}()
	// Do some filtering
	switch network {
	case "tls", "tcp", "ws", "wss":
		laddr, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		listener, err := tls.Listen("tcp", laddr.String(), conf)
		if err != nil {
			return fmt.Errorf("listen tls error. err=%w", err)
		}

		connCloser = listener

		if v := ctx.Value(ctxTestListenAndServeReady); v != nil {
			close(v.(chan any))
		}
		if network == "ws" || network == "wss" {
			return srv.tp.ServeWSS(listener)
		}

		return srv.tp.ServeTLS(listener)
	}

	return transport.ErrNetworkNotSuported
}

// ServeUDP starts serving request on UDP type listener.
func (srv *Server) ServeUDP(l net.PacketConn) error {
	return srv.tp.ServeUDP(l)
}

// ServeTCP starts serving request on TCP type listener.
func (srv *Server) ServeTCP(l net.Listener) error {
	return srv.tp.ServeTCP(l)
}

// ServeTLS starts serving request on TLS type listener.
func (srv *Server) ServeTLS(l net.Listener) error {
	return srv.tp.ServeTLS(l)
}

// ServeWS starts serving request on WS type listener.
func (srv *Server) ServeWS(l net.Listener) error {
	return srv.tp.ServeWS(l)
}

// ServeWS starts serving request on WS type listener.
func (srv *Server) ServeWSS(l net.Listener) error {
	return srv.tp.ServeWSS(l)
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

// Close server handle. UserAgent must be closed for full transaction and transport layer closing.
func (srv *Server) Close() error {
	return nil
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

// OnNoRoute registers no route handler
// default is handling is responding 405 Method Not allowed
// This allows customizing your response for any non handled message
func (srv *Server) OnNoRoute(handler RequestHandler) {
	srv.noRouteHandler = handler
}

// RegisteredMethods returns list of registered handlers.
// Can be used for constructing Allow header
func (srv *Server) RegisteredMethods() []string {
	r := make([]string, len(srv.requestHandlers))
	for k, _ := range srv.requestHandlers {
		r = append(r, k.String())
	}
	return r
}

func (srv *Server) getHandler(method sip.RequestMethod) (handler RequestHandler) {
	handler, ok := srv.requestHandlers[method]
	if !ok {
		return srv.noRouteHandler
	}
	return handler
}

func (srv *Server) defaultUnhandledHandler(req *sip.Request, tx sip.ServerTransaction) {
	srv.log.Warn().Msg("SIP request handler not found")
	res := sip.NewResponseFromRequest(req, 405, "Method Not Allowed", nil)
	// Send response directly and let transaction terminate
	if err := srv.WriteResponse(res); err != nil {
		srv.log.Error().Err(err).Msg("respond '405 Method Not Allowed' failed")
	}
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
