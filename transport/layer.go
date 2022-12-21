package transport

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiraganov/sipgo/parser"
	"github.com/emiraganov/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	ErrNetworkExists = errors.New("network is already served")
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Layer implementation.
type Layer struct {
	transports  map[string]Transport
	listenPorts map[string][]int
	dnsResolver *net.Resolver

	handlers []sip.MessageHandler

	cancelOnce sync.Once
	log        zerolog.Logger

	ConnectionReuse bool
}

// NewLayer creates transport layer.
// hostAddr - address of host
// dns Resolver
func NewLayer(
	dnsResolver *net.Resolver,
) *Layer {
	l := &Layer{
		transports:      make(map[string]Transport),
		listenPorts:     make(map[string][]int),
		dnsResolver:     dnsResolver,
		ConnectionReuse: true,
	}

	l.log = log.Logger.With().Str("caller", "transportlayer").Logger()
	// l.OnMessage(func(msg sip.Message) { l.log.Info().Msg("no handler for message") })
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

	transport := NewUDPTransport(c.LocalAddr().String(), parser.NewParser())
	l.addTransport(transport, port)

	return transport.ServeConn(c, l.handleMessage)
}

// ServeTCP will listen on tcp connection
func (l *Layer) ServeTCP(c net.Listener) error {
	_, port, err := sip.ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	transport := NewTCPTransport(c.Addr().String(), parser.NewParser())
	l.addTransport(transport, port)

	return transport.ServeConn(c, l.handleMessage)
}

// ServeWS will listen on ws connection
func (l *Layer) ServeWS(c net.Listener) error {
	_, port, err := sip.ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	transport := NewWSTransport(c.Addr().String(), parser.NewParser())
	l.addTransport(transport, port)

	return transport.ServeConn(c, l.handleMessage)
}

// Serve on any network. This function will block
func (l *Layer) Serve(ctx context.Context, network string, addr string) error {
	network = strings.ToLower(network)
	_, port, err := sip.ParseAddr(addr)
	if err != nil {
		return err
	}

	_, exists := l.transports[network]
	if exists {
		return ErrNetworkExists
	}

	p := parser.NewParser()

	var t Transport
	switch network {
	case "udp":
		t = NewUDPTransport(addr, p)
	case "tcp":
		t = NewTCPTransport(addr, p)
	case "ws":
		t = NewWSTransport(addr, p)
	case "tls":
		fallthrough
	default:
		return fmt.Errorf("protocol not supported yet")
	}

	// Add transport to list
	l.addTransport(t, port)

	err = t.Serve(l.handleMessage)
	return err
}

func (l *Layer) addTransport(t Transport, port int) {
	network := t.Network()
	if _, ok := l.listenPorts[network]; !ok {
		if l.listenPorts[network] == nil {
			l.listenPorts[network] = make([]int, 0)
		}
		l.listenPorts[network] = append(l.listenPorts[network], port)
	}

	l.transports[network] = t
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
		defer conn.Close()

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
			l.log.Debug().Str("req", req.Method().String()).Msg("Connection ref increment")
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
		return nil, fmt.Errorf("connection does not exist")
	}

	return c, err
}

func (l *Layer) createConnection(network, addr string) (Connection, error) {
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	c, err := transport.CreateConnection(addr)
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
	default:
		return sip.ASCIIToLower(network)
	}
}
