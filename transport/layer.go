package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	ErrNetworkNotSuported = errors.New("protocol not supported")
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Layer implementation.
type Layer struct {
	udp *UDPTransport
	tcp *TCPTransport
	tls *TLSTransport
	ws  *WSTransport
	wss *WSSTransport

	transports map[string]Transport

	listenPorts   map[string][]int
	listenPortsMu sync.Mutex
	dnsResolver   *net.Resolver

	handlers []sip.MessageHandler

	log zerolog.Logger

	// Parser used by transport layer. It can be overrided before setuping network transports
	Parser sip.Parser
	// ConnectionReuse will force connection reuse when passing request
	ConnectionReuse bool
}

// NewLayer creates transport layer.
// dns Resolver
// sip parser
// tls config - can be nil to use default tls
func NewLayer(
	dnsResolver *net.Resolver,
	sipparser sip.Parser,
	tlsConfig *tls.Config,
) *Layer {
	l := &Layer{
		transports:      make(map[string]Transport),
		listenPorts:     make(map[string][]int),
		dnsResolver:     dnsResolver,
		Parser:          sipparser,
		ConnectionReuse: true,
	}

	l.log = log.Logger.With().Str("caller", "transportlayer").Logger()

	// Make some default transports available.
	l.udp = NewUDPTransport(sipparser)
	l.tcp = NewTCPTransport(sipparser)
	// TODO. Using default dial tls, but it needs to configurable via client
	l.tls = NewTLSTransport(sipparser, tlsConfig)
	l.ws = NewWSTransport(sipparser)
	// TODO. Using default dial tls, but it needs to configurable via client
	l.wss = NewWSSTransport(sipparser, tlsConfig)

	// Fill map for fast access
	l.transports["udp"] = l.udp
	l.transports["tcp"] = l.tcp
	l.transports["tls"] = l.tls
	l.transports["ws"] = l.ws
	l.transports["wss"] = l.wss

	return l
}

// OnMessage is main function which will be called on any new message by transport layer
func (l *Layer) OnMessage(h sip.MessageHandler) {
	// if l.handler != nil {
	// 	// Make sure appending
	// 	next := l.handler
	// 	l.handler = func(m sip.Message) {
	// 		h(m)
	// 		next(m)
	// 	}
	// 	return
	// }

	// l.handler = h

	l.handlers = append(l.handlers, h)
}

// handleMessage is transport layer for handling messages
func (l *Layer) handleMessage(msg sip.Message) {
	// We have to consider
	// https://datatracker.ietf.org/doc/html/rfc3261#section-18.2.1 for some message editing
	// Proxy further to other

	// 18.1.2 Receiving Responses
	// States that transport should find transaction and if not, it should still forward message to core
	// l.handler(msg)
	for _, h := range l.handlers {
		h(msg)
	}
}

// ServeUDP will listen on udp connection
func (l *Layer) ServeUDP(c net.PacketConn) error {
	_, port, err := sip.ParseAddr(c.LocalAddr().String())
	if err != nil {
		return err
	}

	l.addListenPort("udp", port)

	return l.udp.Serve(c, l.handleMessage)
}

// ServeTCP will listen on tcp connection
func (l *Layer) ServeTCP(c net.Listener) error {
	_, port, err := sip.ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("tcp", port)

	return l.tcp.Serve(c, l.handleMessage)
}

// ServeWS will listen on ws connection
func (l *Layer) ServeWS(c net.Listener) error {
	_, port, err := sip.ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("ws", port)

	return l.ws.Serve(c, l.handleMessage)
}

// ServeTLS will listen on tcp connection
func (l *Layer) ServeTLS(c net.Listener) error {
	_, port, err := sip.ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("tls", port)
	return l.tls.Serve(c, l.handleMessage)
}

// ServeWSS will listen on wss connection
func (l *Layer) ServeWSS(c net.Listener) error {
	_, port, err := sip.ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("wss", port)

	return l.wss.Serve(c, l.handleMessage)
}

// Serve on any network. This function will block
// Network supported: udp, tcp, ws
func (l *Layer) ListenAndServe(ctx context.Context, network string, addr string) error {
	network = strings.ToLower(network)
	// Do some filtering
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
				l.log.Error().Err(err).Msg("Failed to close listener")
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
		return l.ServeUDP(udpConn)

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
		// and uses listener to buffer
		if network == "ws" {
			return l.ServeWS(conn)
		}

		return l.ServeTCP(conn)
	}
	return ErrNetworkNotSuported
}

// Serve on any tls network. This function will block
// Network supported: tcp
func (l *Layer) ListenAndServeTLS(ctx context.Context, network string, addr string, conf *tls.Config) error {
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
				l.log.Error().Err(err).Msg("Failed to close listener")
			}

		}
	}()
	// Do some filtering
	switch network {
	case "tls", "tcp", "wss":
		laddr, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return fmt.Errorf("fail to resolve address. err=%w", err)
		}

		listener, err := tls.Listen("tcp", laddr.String(), conf)
		if err != nil {
			return fmt.Errorf("listen tls error. err=%w", err)
		}

		connCloser = listener
		if network == "wss" {
			return l.ServeWSS(listener)
		}

		return l.ServeTLS(listener)
	}

	return ErrNetworkNotSuported
}

func (l *Layer) addListenPort(network string, port int) {
	l.listenPortsMu.Lock()
	defer l.listenPortsMu.Unlock()

	if _, ok := l.listenPorts[network]; !ok {
		if l.listenPorts[network] == nil {
			l.listenPorts[network] = make([]int, 0)
		}
		l.listenPorts[network] = append(l.listenPorts[network], port)
	}
}

func (l *Layer) WriteMsg(msg sip.Message) error {
	network := msg.Transport()
	addr := msg.Destination()
	return l.WriteMsgTo(msg, addr, network)
}

func (l *Layer) WriteMsgTo(msg sip.Message, addr string, network string) error {
	/*s
	// Client sending request, or we are sending responses
	To consider
		18.2.1
		When the server transport receives a request over any transport, it
		MUST examine the value of the "sent-by" parameter in the top Via
		header field value.
		If the host portion of the "sent-by" parameter
	contains a domain name, or if it contains an IP address that differs
	from the packet source address, the server MUST add a "received"
	parameter to that Via header field value.  This parameter MUST
	contain the source address from which the packet was received.
	*/

	var conn Connection
	var err error

	switch m := msg.(type) {
	// RFC 3261 - 18.1.1.
	// 	TODO
	// 	If a request is within 200 bytes of the path MTU, or if it is larger
	//    than 1300 bytes and the path MTU is unknown, the request MUST be sent
	//    using an RFC 2914 [43] congestion controlled transport protocol, such
	//    as TCP. If this causes a change in the transport protocol from the
	//    one indicated in the top Via, the value in the top Via MUST be
	//    changed.
	case *sip.Request:
		//Every new request must be handled in seperate connection
		conn, err = l.ClientRequestConnection(m)
		if err != nil {
			return err
		}

		// Reference counting should prevent us closing connection too early
		defer conn.TryClose()

		// RFC 3261 - 18.2.2.
	case *sip.Response:

		conn, err = l.GetConnection(network, addr)
		if err != nil {
			return err
		}
	}

	if err := conn.WriteMsg(msg); err != nil {
		return err
	}

	// transport, ok := l.transports[network]
	// if !ok {
	// 	return fmt.Errorf("transport %s is not supported", network)
	// }

	// raddr, err := transport.ResolveAddr(addr)
	// if err != nil {
	// 	return err
	// }

	// err = transport.WriteMsg(msg, raddr)
	// if err != nil {
	// 	err = fmt.Errorf("send SIP message through %s protocol to %s: %w", network, addr, err)
	// }
	return err
}

// ClientRequestConnection is based on
// https://www.rfc-editor.org/rfc/rfc3261#section-18.1.1
// It is wrapper for getting and creating connection
func (l *Layer) ClientRequestConnection(req *sip.Request) (c Connection, err error) {
	network := NetworkToLower(req.Transport())
	addr := req.Destination()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("build address target for %s: %w", addr, err)
	}
	// dns srv lookup
	if net.ParseIP(host) == nil {
		ctx := context.Background()
		if _, addrs, err := l.dnsResolver.LookupSRV(ctx, "sip", network, host); err == nil && len(addrs) > 0 {
			a := addrs[0]
			addr = a.Target[:len(a.Target)-1] + ":" + strconv.Itoa(int(a.Port))
		}
	}

	viaHop, exists := req.Via()
	if !exists {
		return nil, fmt.Errorf("missing Via Header")
	}
	// rewrite sent-by port
	if viaHop.Port <= 0 {
		if ports, ok := l.listenPorts[network]; ok {
			port := ports[rand.Intn(len(ports))]
			viaHop.Port = port
		} else {
			defPort := sip.DefaultPort(network)
			viaHop.Port = int(defPort)
		}
	}

	if l.ConnectionReuse {
		viaHop.Params.Add("alias", "")
		c, _ = l.getConnection(network, addr)
		if c != nil {
			//Increase reference. This should prevent client connection early drop
			l.log.Debug().Str("req", req.Method.String()).Msg("Connection ref increment")
			c.Ref(1)
			return c, nil
		}
	}

	c, err = l.createConnection(network, addr)
	return c, err
}

// GetConnection gets existing or creates new connection based on addr
func (l *Layer) GetConnection(network, addr string) (Connection, error) {
	network = NetworkToLower(network)
	return l.getConnection(network, addr)
}

func (l *Layer) CreateConnection(network, addr string) (Connection, error) {
	network = NetworkToLower(network)
	return l.createConnection(network, addr)
}

func (l *Layer) getConnection(network, addr string) (Connection, error) {
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	c, err := transport.GetConnection(addr)
	if err == nil && c == nil {
		return nil, fmt.Errorf("connection %q does not exist", addr)
	}

	return c, err
}

func (l *Layer) createConnection(network, addr string) (Connection, error) {
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	// If there are no transport handlers registered for handling connection message
	// this message will be dropped
	c, err := transport.CreateConnection(addr, l.handleMessage)
	return c, err
}

func (l *Layer) Close() error {
	var werr error
	for _, t := range l.transports {
		if err := t.Close(); err != nil {
			// For now dump last error
			werr = err
		}
	}
	return werr
}

func IsReliable(network string) bool {
	switch network {
	case "tcp", "tls", "TCP", "TLS":
		return true
	default:
		return false
	}
}

func IsStreamed(network string) bool {
	switch network {
	case "tcp", "tls", "TCP", "TLS":
		return true
	default:
		return false
	}
}

// NetworkToLower is faster function converting UDP, TCP to udp, tcp
func NetworkToLower(network string) string {
	// Switch is faster then lower
	switch network {
	case "UDP":
		return "udp"
	case "TCP":
		return "tcp"
	case "TLS":
		return "tls"
	case "WS":
		return "ws"
	default:
		return sip.ASCIIToLower(network)
	}
}
