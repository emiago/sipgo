package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
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

	// ConnectionReuse will force connection reuse when passing request
	ConnectionReuse bool
}

// NewLayer creates transport layer.
// dns Resolver
// sip parser
// tls config - can be nil to use default tls
func NewLayer(
	dnsResolver *net.Resolver,
	sipparser *sip.Parser,
	tlsConfig *tls.Config,
) *Layer {
	l := &Layer{
		transports:      make(map[string]Transport),
		listenPorts:     make(map[string][]int),
		dnsResolver:     dnsResolver,
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

func (l *Layer) GetListenPort(network string) int {
	ports, _ := l.listenPorts[network]
	if len(ports) > 0 {
		return ports[0]
	}
	return 0
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
		var ctx = context.Background()
		//Every new request must be handled in seperate connection
		conn, err = l.ClientRequestConnection(ctx, m)
		if err != nil {
			return err
		}

		// Reference counting should prevent us closing connection too early
		defer conn.TryClose()

	case *sip.Response:

		conn, err = l.GetConnection(network, addr)
		if err != nil {
			return err
		}

		defer conn.TryClose()
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
//
// In case req destination is DNS resolved, destination will be cached or in
// other words SetDestination will be called
func (l *Layer) ClientRequestConnection(ctx context.Context, req *sip.Request) (c Connection, err error) {
	network := NetworkToLower(req.Transport())
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	// Resolve our remote address
	a := req.Destination()
	host, port, err := sip.ParseAddr(a)
	if err != nil {
		return nil, fmt.Errorf("build address target for %s: %w", a, err)
	}

	// dns srv lookup

	raddr := Addr{
		IP:   net.ParseIP(host),
		Port: port,
	}
	if raddr.IP == nil {
		if err := l.resolveAddr(ctx, network, host, &raddr); err != nil {
			return nil, err
		}

		// Save destination in request to avoid repeated resolving
		req.SetDestination(raddr.String())
	}

	// Now use Via header to determine our local address
	// Here is from RFC statement:
	//   Before a request is sent, the client transport MUST insert a value of
	//   the "sent-by" field into the Via header field.  This field contains
	//   an IP address or host name, and port.
	viaHop, exists := req.Via()
	if !exists {
		// NOTE: We are enforcing that client creates this header
		return nil, fmt.Errorf("missing Via Header")
	}

	laddr := Addr{
		IP: net.ParseIP(viaHop.Host),
		// IP:   lIP,
		Port: viaHop.Port,
	}

	// TODO refactor code below
	if l.ConnectionReuse {
		viaHop.Params.Add("alias", "")
		addr := raddr.String()

		c, _ := transport.GetConnection(addr)
		if c != nil {
			// Update Via sent by
			// TODO avoid this parsing
			laddr := c.LocalAddr()
			network := laddr.Network()
			laddrStr := laddr.String()

			// TODO handle broadcast address
			host, port, err := sip.ParseAddr(laddrStr)
			if err != nil {
				return nil, fmt.Errorf("fail to parse local connection address network=%s addr=%s: %w", network, laddrStr, err)
			}

			// In case client forced some host (like external IP) we do not want to overwrite
			// Currently we always have this set as resolved IP
			if viaHop.Host == "" {
				viaHop.Host = host

			}

			viaHop.Port = port
			return c, nil
		}

		// In case client handle sets address same as UDP listen addr
		// try grabbing listener for udp and send packet connectionless
		if c == nil && network == "udp" && laddr.IP != nil && laddr.Port > 0 {
			c, _ = transport.GetConnection(laddr.String())

			if c != nil {
				viaHop.Host = laddr.IP.String()
				viaHop.Port = laddr.Port

				// DO NOT USE unspecified IP with client handle
				// switch {
				// case laddr.IP.IsUnspecified():
				// 	l.log.Warn().Msg("External Via IP address is unspecified for UDP. Using 127.0.0.1")
				// 	viaHop.Host = "127.0.0.1" // TODO use resolve IP
				// }
				return c, nil
			}
		}
		l.log.Debug().Str("addr", addr).Msg("Active connection not found")
	}

	l.log.Debug().Str("host", viaHop.Host).Int("port", viaHop.Port).Msg("Via header used for creating connection")

	c, err = transport.CreateConnection(ctx, laddr, raddr, l.handleMessage)
	if err != nil {
		return nil, err
	}

	// TODO refactor this
	switch {
	case viaHop.Host == "" || laddr.IP == nil: // If not specified by UAC we will override Via sent-by
		fallthrough
	case viaHop.Port == 0: // We still may need to rewrite sent-by port
		// TODO avoid this parsing
		l := c.LocalAddr()
		laddrStr := l.String()

		host, port, err = sip.ParseAddr(laddrStr)
		if err != nil {
			return nil, fmt.Errorf("fail to parse local connection address network=%s addr=%s: %w", network, laddrStr, err)
		}

		if viaHop.Host != "" {
			viaHop.Host = host
		}
		viaHop.Port = port
	}
	return c, nil
}

func (l *Layer) resolveAddr(ctx context.Context, network string, host string, addr *Addr) error {
	defer func(start time.Time) {
		if dur := time.Since(start); dur > 50*time.Millisecond {
			l.log.Warn().Dur("dur", dur).Msg("DNS resolution is slow")
		}
	}(time.Now())

	l.log.Debug().Str("host", host).Msg("DNS Resolving")
	// We need to try local resolving.
	ip, err := net.ResolveIPAddr("ip", host)
	if err == nil {
		addr.IP = ip.IP
		return nil
	}
	log.Debug().Err(err).Msg("IP addr resolving failed, doing via dns resolver")

	var lookupnet string
	switch network {
	case "udp":
		lookupnet = "udp"
	default:
		lookupnet = "tcp"
	}

	_, addrs, err := l.dnsResolver.LookupSRV(ctx, "sip", lookupnet, host)
	if err != nil {
		return fmt.Errorf("fail to resolve target for %q: %w", host, err)
	}
	a := addrs[0]
	addr.IP = net.ParseIP(a.Target[:len(a.Target)-1])
	addr.Port = int(a.Port)
	return nil
}

// GetConnection gets existing or creates new connection based on addr
func (l *Layer) GetConnection(network, addr string) (Connection, error) {
	network = NetworkToLower(network)
	return l.getConnection(network, addr)
}

func (l *Layer) getConnection(network, addr string) (Connection, error) {
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	l.log.Debug().Str("network", network).Str("addr", addr).Msg("getting connection")
	c, err := transport.GetConnection(addr)
	if err == nil && c == nil {
		return nil, fmt.Errorf("connection %q does not exist", addr)
	}

	return c, err
}

func (l *Layer) Close() error {
	l.log.Debug().Msg("Layer is closing")
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
	case "udp", "UDP":
		return false
	default:
		return true
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
	case "WSS":
		return "wss"
	default:
		return sip.ASCIIToLower(network)
	}
}
