package sipgo

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/emiago/sipgo/sip"
)

var (
	// Used only for testing, better way is to pass listener with Serve{Transport}
	ListenReadyCtxKey = "ListenReadyCtxKey"
)

type ListenReadyCtxValue chan struct{}
type ListenReadyFuncCtxValue func(network string, addr string)

func listenReadyCtx(ctx context.Context, network string, addr string) {
	if v := ctx.Value(ListenReadyCtxKey); v != nil {
		switch vv := v.(type) {
		case ListenReadyCtxValue:
			vv <- struct{}{}
		case ListenReadyFuncCtxValue:
			vv(network, addr)
		}
	}
}

// RequestHandler is a callback that will be called on the incoming request
type RequestHandler func(req *sip.Request, tx sip.ServerTransaction)

// Server is a SIP server
type Server struct {
	*UserAgent

	// requestHandlers map of all registered request handlers
	requestHandlers map[sip.RequestMethod]RequestHandler
	noRouteHandler  RequestHandler

	log *slog.Logger

	requestMiddlewares []func(r *sip.Request)
}

type ServerOption func(s *Server) error

// WithServerLogger allows customizing server logger
func WithServerLogger(logger *slog.Logger) ServerOption {
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
	s.tx.OnRequest(s.handleRequest)
	return s, nil
}

func newBaseServer(ua *UserAgent, options ...ServerOption) (*Server, error) {
	s := &Server{
		UserAgent: ua,
		// userAgent:           "SIPGO",
		// dnsResolver:         net.DefaultResolver,
		requestMiddlewares: make([]func(r *sip.Request), 0),
		requestHandlers:    make(map[sip.RequestMethod]RequestHandler),
		// log:                 log.Logger.With().Str("caller", "Server").Logger(),
		log: sip.DefaultLogger().With("caller", "Server"),
	}
	for _, o := range options {
		if err := o(s); err != nil {
			return nil, err
		}
	}

	s.noRouteHandler = s.defaultUnhandledHandler

	return s, nil
}

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
				srv.log.Error("Failed to close listener", "error", err)
			}

		}
	}()

	switch network {
	case "udp", "udp4", "udp6":
		// resolve local UDP endpoint
		laddr, err := net.ResolveUDPAddr(network, addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		udpConn, err := net.ListenUDP(network, laddr)
		if err != nil {
			return fmt.Errorf("listen udp error. err=%w", err)
		}

		connCloser = udpConn
		listenReadyCtx(ctx, network, udpConn.LocalAddr().String())
		return srv.tp.ServeUDP(udpConn)

	case "tcp", "tcp4", "tcp6":
		laddr, err := net.ResolveTCPAddr(network, addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		conn, err := net.ListenTCP(network, laddr)
		if err != nil {
			return fmt.Errorf("listen tcp error. err=%w", err)
		}

		connCloser = conn
		listenReadyCtx(ctx, network, conn.Addr().String())

		return srv.tp.ServeTCP(conn)
	case "ws", "ws4", "ws6":
		ipv := network[2:]
		network = "tcp" + ipv
		laddr, err := net.ResolveTCPAddr(network, addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		conn, err := net.ListenTCP(network, laddr)
		if err != nil {
			return fmt.Errorf("listen tcp error. err=%w", err)
		}

		connCloser = conn
		listenReadyCtx(ctx, network, conn.Addr().String())
		// and uses listener to buffer
		return srv.tp.ServeWS(conn)
	}
	return sip.ErrTransportNotSuported
}

// Serve will fire all listeners that are secured.
// Network supported: tls, wss, tcp, tcp4, tcp6, ws, ws4, ws6
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
				srv.log.Error("Failed to close listener", "error", err)
			}

		}
	}()
	// Support explicitp ipv4 vs ipv6
	tcpNetwork := "tcp"
	switch network {
	case "tcp":
		tcpNetwork = "tcp"
		network = "tls"
	case "ws":
		tcpNetwork = "tcp"
		network = "wss"
	case "tcp4", "ws4":
		tcpNetwork = "tcp4"
		network = "tls"
	case "tcp6", "ws6":
		tcpNetwork = "tcp6"
		network = "wss"
	}

	switch network {
	case "tls", "wss":
		laddr, err := net.ResolveTCPAddr(tcpNetwork, addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		listener, err := tls.Listen(tcpNetwork, laddr.String(), conf)
		if err != nil {
			return fmt.Errorf("listen tls error. err=%w", err)
		}

		connCloser = listener
		listenReadyCtx(ctx, network, listener.Addr().String())

		if network == "wss" {
			return srv.tp.ServeWSS(listener)
		}

		return srv.tp.ServeTLS(listener)
	}

	return sip.ErrTransportNotSuported
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

// handleRequest is handling transaction layer
func (srv *Server) handleRequest(req *sip.Request, tx *sip.ServerTx) {
	for _, mid := range srv.requestMiddlewares {
		mid(req)
	}

	handler := srv.getHandler(req.Method)
	handler(req, tx)
	if tx != nil {
		// Must be called to prevent any transaction leaks
		tx.TerminateGracefully()
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
	r := make([]string, 0, len(srv.requestHandlers))
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
	srv.log.Warn("SIP request handler not found", "method", req.Method)
	res := sip.NewResponseFromRequest(req, 405, "Method Not Allowed", nil)
	if err := tx.Respond(res); err != nil {
		srv.log.Error("respond '405 Method Not Allowed' failed", "error", err)
	}
}

// serveRequest can be used as middleware for preprocessing message
func (srv *Server) serveRequest(f func(r *sip.Request)) {
	srv.requestMiddlewares = append(srv.requestMiddlewares, f)
}

// Transport is function to get transport layer of server
// Can be used for modifying
func (srv *Server) TransportLayer() *sip.TransportLayer {
	return srv.tp
}
