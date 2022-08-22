package sipgo

import (
	"context"
	"net"

	"github.com/emiraganov/sipgo/sip"
	"github.com/emiraganov/sipgo/transaction"
	"github.com/emiraganov/sipgo/transport"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// RequestHandler is a callback that will be called on the incoming request
type RequestHandler func(req *sip.Request, tx sip.ServerTransaction)

// Server is a SIP server
type Server struct {
	tp          *transport.Layer
	tx          *transaction.Layer
	ip          net.IP
	dnsResolver *net.Resolver
	userAgent   string

	// requestHandlers map of all registered request handlers
	requestHandlers map[sip.RequestMethod]RequestHandler
	listeners       map[string]string //addr:network

	//Serve request is middleware run before any request received
	serveMessage func(m sip.Message)

	log zerolog.Logger
}

type ServerOption func(s *Server) error

func WithLogger(logger zerolog.Logger) ServerOption {
	return func(s *Server) error {
		s.log = logger
		return nil
	}
}

func WithIP(ip string) ServerOption {
	return func(s *Server) error {
		host, _, err := net.SplitHostPort(ip)
		if err != nil {
			return err
		}
		addr, err := net.ResolveIPAddr("ip", host)
		if err != nil {
			return err
		}
		s.ip = addr.IP
		return nil
	}
}

func WithDNSResolver(r *net.Resolver) ServerOption {
	return func(s *Server) error {
		s.dnsResolver = r
		return nil
	}
}

func WithUDPDNSResolver(dns string) ServerOption {
	return func(s *Server) error {
		s.dnsResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, "udp", dns)
			},
		}
		return nil
	}
}

func WithUserAgent(ua string) ServerOption {
	return func(s *Server) error {
		s.userAgent = ua
		return nil
	}
}

// NewServer creates new instance of SIP server.
func NewServer(options ...ServerOption) (*Server, error) {
	s := &Server{
		userAgent:       "SIPGO",
		dnsResolver:     net.DefaultResolver,
		requestHandlers: make(map[sip.RequestMethod]RequestHandler),
		listeners:       make(map[string]string),
		log:             log.Logger.With().Str("caller", "Server").Logger(),
	}
	for _, o := range options {
		if err := o(s); err != nil {
			return nil, err
		}
	}

	if s.ip == nil {
		v, err := sip.ResolveSelfIP()
		if err != nil {
			return nil, err
		}
		s.ip = v
	}

	s.tp = transport.NewLayer(s.dnsResolver)
	s.tx = transaction.NewLayer(s.tp, s.onRequest)

	return s, nil
}

// onRequest gets request from Transaction layer
func (srv *Server) onRequest(req *sip.Request, tx sip.ServerTransaction) {
	go srv.handleRequest(req, tx)
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

// handleRequest must be run in seperate goroutine
func (srv *Server) handleRequest(req *sip.Request, tx sip.ServerTransaction) {
	handler := srv.getHandler(req.Method())

	if handler == nil {
		srv.log.Warn().Msg("SIP request handler not found")
		res := sip.NewResponseFromRequest(req, 405, "Method Not Allowed", nil)
		if err := srv.Send(res); err != nil {
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

// TransactionRequest sends sip request and initializes client transaction
// It prepends Via header by default
func (srv *Server) TransactionRequest(req *sip.Request) (sip.ClientTransaction, error) {
	/*
		To consider
		18.2.1 We could have to change network if message is to large for UDP
	*/

	return srv.tx.Request(req)
}

// TransactionReply sends
func (srv *Server) TransactionReply(res *sip.Response) (sip.ServerTransaction, error) {
	return srv.tx.Respond(res)
}

// Send will proxy message to transport layer. Use it in stateless mode
func (srv *Server) Send(msg sip.Message) error {
	return srv.tp.WriteMsg(msg)
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

// ServeMessage can be used as middleware for preprocessing message
// It process all received requests and all received responses.
// NOTE: It does not serve any client request or server responses.
func (srv *Server) ServeMessage(f func(m sip.Message)) {
	srv.tp.OnMessage(f)
}

// Transport is function to get transport layer of server
// Can be used for modifying
func (srv *Server) TransportLayer() *transport.Layer {
	return srv.tp
}
