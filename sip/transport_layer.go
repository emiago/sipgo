package sip

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

var (
	tlsEmptyConf tls.Config

	// Errors
	ErrTransportNotSuported = errors.New("protocol not supported")
)

// TransportLayer implementation.
type TransportLayer struct {
	udp *transportUDP
	tcp *transportTCP
	tls *transportTLS
	ws  *transportWS
	wss *transportWSS

	transports map[string]Transport

	listenPorts   map[string][]int
	listenPortsMu sync.Mutex
	dnsResolver   *net.Resolver

	handlers []MessageHandler

	log *slog.Logger

	// ConnectionReuse will force connection reuse when passing request
	ConnectionReuse bool

	// PreferSRV does always SRV lookup first
	DNSPreferSRV bool
}

type TransportLayerOption func(l *TransportLayer)

func WithTransportLayerLogger(logger *slog.Logger) TransportLayerOption {
	return func(l *TransportLayer) {
		l.log = logger
	}
}

// NewLayer creates transport layer.
// dns Resolver
// sip parser
// tls config - can be nil to use default tls
func NewTransportLayer(
	dnsResolver *net.Resolver,
	sipparser *Parser,
	tlsConfig *tls.Config,
	option ...TransportLayerOption,
) *TransportLayer {
	l := &TransportLayer{
		transports:      make(map[string]Transport),
		listenPorts:     make(map[string][]int),
		dnsResolver:     dnsResolver,
		ConnectionReuse: true,
		log:             slog.With("caller", "TransportLayer"),
	}

	for _, o := range option {
		o(l)
	}

	if tlsConfig == nil {
		// Use empty tls config
		tlsConfig = &tlsEmptyConf
	}

	// Exporting transport configuration
	// UDP
	l.udp = &transportUDP{
		log: l.log.With("caller", "Transport<UDP>"),
	}
	l.udp.init(sipparser)

	// TCP
	l.tcp = &transportTCP{
		log: l.log.With("caller", "Transport<TCP>"),
	}
	l.tcp.init(sipparser)

	// TLS
	// TODO. Using default dial tls, but it needs to configurable via client
	l.tls = &transportTLS{
		transportTCP: &transportTCP{
			log: l.log.With("caller", "Transport<TLS>"),
		},
	}
	l.tls.init(sipparser, tlsConfig)

	// WS
	l.ws = &transportWS{
		log: l.log.With("caller", "Transport<WS>"),
	}
	l.ws.init(sipparser)

	// WSS
	// TODO. Using default dial tls, but it needs to configurable via client
	l.wss = &transportWSS{
		transportWS: &transportWS{
			log: l.log.With("caller", "Transport<WSS>"),
		},
	}
	l.wss.init(sipparser, tlsConfig)

	// Fill map for fast access
	l.transports["udp"] = l.udp
	l.transports["tcp"] = l.tcp
	l.transports["tls"] = l.tls
	l.transports["ws"] = l.ws
	l.transports["wss"] = l.wss

	return l
}

// OnMessage is main function which will be called on any new message by transport layer
// Consider there is no concurency and you need to make sure that you do not block too long
// This is intentional as higher concurency can slow things
func (l *TransportLayer) OnMessage(h MessageHandler) {
	// if l.handler != nil {
	// 	// Make sure appending
	// 	next := l.handler
	// 	l.handler = func(m Message) {
	// 		h(m)
	// 		next(m)
	// 	}
	// 	return
	// }

	// l.handler = h
	l.handlers = append(l.handlers, h)
}

// handleMessage is transport layer for handling messages
func (l *TransportLayer) handleMessage(msg Message) {
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
func (l *TransportLayer) ServeUDP(c net.PacketConn) error {
	_, port, err := ParseAddr(c.LocalAddr().String())
	if err != nil {
		return err
	}

	l.addListenPort("udp", port)

	return l.udp.Serve(c, l.handleMessage)
}

// ServeTCP will listen on tcp connection
func (l *TransportLayer) ServeTCP(c net.Listener) error {
	_, port, err := ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("tcp", port)

	return l.tcp.Serve(c, l.handleMessage)
}

// ServeWS will listen on ws connection
func (l *TransportLayer) ServeWS(c net.Listener) error {
	_, port, err := ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("ws", port)

	return l.ws.Serve(c, l.handleMessage)
}

// ServeTLS will listen on tcp connection
func (l *TransportLayer) ServeTLS(c net.Listener) error {
	_, port, err := ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}

	l.addListenPort("tls", port)
	return l.tls.Serve(c, l.handleMessage)
}

// ServeWSS will listen on wss connection
func (l *TransportLayer) ServeWSS(c net.Listener) error {
	_, port, err := ParseAddr(c.Addr().String())
	if err != nil {
		return err
	}
	l.addListenPort("wss", port)

	return l.wss.Serve(c, l.handleMessage)
}

func (l *TransportLayer) addListenPort(network string, port int) {
	l.listenPortsMu.Lock()
	defer l.listenPortsMu.Unlock()

	if _, ok := l.listenPorts[network]; !ok {
		if l.listenPorts[network] == nil {
			l.listenPorts[network] = make([]int, 0)
		}
		l.listenPorts[network] = append(l.listenPorts[network], port)
	}
}

func (l *TransportLayer) GetListenPort(network string) int {
	network = NetworkToLower(network)
	ports, _ := l.listenPorts[network]
	if len(ports) > 0 {
		return ports[0]
	}
	return 0
}

func (l *TransportLayer) ListenPorts(network string) []int {
	l.listenPortsMu.Lock()
	defer l.listenPortsMu.Unlock()

	network = NetworkToLower(network)
	ports, _ := l.listenPorts[network]
	return append(ports[:0:0], ports...) // Faster clone without cloning other slice fields
}

func (l *TransportLayer) WriteMsg(msg Message) error {
	network := msg.Transport()
	addr := msg.Destination()
	return l.WriteMsgTo(msg, addr, network)
}

func (l *TransportLayer) WriteMsgTo(msg Message, addr string, network string) error {
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
	case *Request:
		var ctx = context.Background()
		//Every new request must be handled in seperate connection
		conn, err = l.ClientRequestConnection(ctx, m)
		if err != nil {
			return err
		}

		// Reference counting should prevent us closing connection too early
		defer conn.TryClose()

	case *Response:

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
func (l *TransportLayer) ClientRequestConnection(ctx context.Context, req *Request) (c Connection, err error) {
	network := NetworkToLower(req.Transport())
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	// Resolve our remote address
	a := req.Destination()
	host, port, err := ParseAddr(a)
	if err != nil {
		return nil, fmt.Errorf("build address target for %s: %w", a, err)
	}

	// dns srv lookup

	raddr := Addr{
		IP:       net.ParseIP(host),
		Port:     port,
		Hostname: host,
	}

	if raddr.Port == 0 {
		// Use default port for transport
		raddr.Port = DefaultPort(network)
	}

	if raddr.IP == nil {
		if err := l.resolveAddr(ctx, network, host, &raddr); err != nil {
			return nil, err
		}

		// Save destination in request to avoid repeated resolving
		// This also solves problem where subsequent request like NON 2xx ACK can
		// send on same destination without resolving again.
		req.SetDestination(raddr.String())
	}

	// Now use Via header to determine our local address
	// Here is from RFC statement:
	//   Before a request is sent, the client transport MUST insert a value of
	//   the "sent-by" field into the Via header field.  This field contains
	//   an IP address or host name, and port.
	viaHop := req.Via()
	if viaHop == nil {
		// NOTE: We are enforcing that client creates this header
		return nil, fmt.Errorf("missing Via Header")
	}

	laddr := Addr{
		IP:   net.ParseIP(viaHop.Host),
		Port: viaHop.Port,
	}

	// Always check does connection exists if full IP:port provided
	// This is probably client forcing host:port
	if laddr.IP != nil && laddr.Port > 0 {
		c = transport.GetConnection(laddr.String())
		if c != nil {
			if req.AdvertisedHost != "" {
				viaHop.Host = req.AdvertisedHost
			}
			return c, nil
		}
	} else if l.ConnectionReuse {
		// viaHop.Params.Add("alias", "")
		addr := raddr.String()

		c = transport.GetConnection(addr)
		if c != nil {
			// Update Via sent by
			la := c.LocalAddr()
			network := la.Network()
			laStr := la.String()

			// TODO handle broadcast address
			// TODO avoid this parsing
			host, port, err := ParseAddr(laStr)
			if err != nil {
				return nil, fmt.Errorf("fail to parse local connection address network=%s addr=%s: %w", network, laStr, err)
			}

			// https://datatracker.ietf.org/doc/html/rfc3261#section-18
			// Before a request is sent, the client transport MUST insert a value of
			// the "sent-by" field into the Via header field.  This field contains
			// an IP address or host name, and port.  The usage of an FQDN is
			// RECOMMENDED.
			if viaHop.Host == "" {
				viaHop.Host = host
			}
			viaHop.Port = port
			return c, nil
		}

		// In case client handle sets address same as UDP listen addr
		// try grabbing listener for udp and send packet connectionless
		/* if c == nil && network == "udp" && laddr.IP != nil && laddr.Port > 0 {
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
		} */
		l.log.Debug("Active connection not found", "addr", addr, "raddr", raddr.String())
	}
	l.log.Debug("Via header used for creating connection", "host", viaHop.Host, "port", viaHop.Port, "network", network)

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
		la := c.LocalAddr()
		laStr := la.String()

		host, port, err = ParseAddr(laStr)
		if err != nil {
			return nil, fmt.Errorf("fail to parse local connection address network=%s addr=%s: %w", network, laStr, err)
		}

		// https://datatracker.ietf.org/doc/html/rfc3261#section-18
		// Before a request is sent, the client transport MUST insert a value of
		// the "sent-by" field into the Via header field.  This field contains
		// an IP address or host name, and port.  The usage of an FQDN is
		// RECOMMENDED.
		if viaHop.Host == "" {
			viaHop.Host = host
		}
		viaHop.Port = port
	}
	return c, nil
}

func (l *TransportLayer) resolveAddr(ctx context.Context, network string, host string, addr *Addr) error {
	log := l.log
	defer func(start time.Time) {
		if dur := time.Since(start); dur > 50*time.Millisecond {
			l.log.Warn("DNS resolution is slow", "dur", dur)
		}
	}(time.Now())

	if l.DNSPreferSRV {
		err := l.resolveAddrSRV(ctx, network, host, addr)
		if err == nil {
			return nil
		}
		log.Warn("Doing SRV lookup failed.", "host", host, "error", err)
		return l.resolveAddrIP(ctx, host, addr)
	}

	err := l.resolveAddrIP(ctx, host, addr)
	if err == nil {
		return nil
	}

	log.Info("IP addr resolving failed, doing via dns SRV resolver...", "error", err)
	return l.resolveAddrSRV(ctx, network, host, addr)
}

func (l *TransportLayer) resolveAddrIP(ctx context.Context, hostname string, addr *Addr) error {
	l.log.Debug("DNS Resolving", "host", hostname)

	// Do local resolving
	ips, err := l.dnsResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		// Should not happen
		return fmt.Errorf("lookup ip addr did not return any ip addr")
	}

	for _, ip := range ips {
		if len(ip.IP) == net.IPv4len {
			addr.IP = ip.IP
			return nil
		}
	}
	addr.IP = ips[0].IP
	return nil
}

func (l *TransportLayer) resolveAddrSRV(ctx context.Context, network string, hostname string, addr *Addr) error {
	log := l.log
	var proto string
	switch network {
	case "udp", "udp4", "udp6":
		proto = "udp"
	case "tls":
		proto = "tls"
	default:
		proto = "tcp"
	}

	log.Debug("Doing SRV lookup", "proto", proto, "host", hostname)

	// The returned records are sorted by priority and randomized
	// by weight within a priority.
	_, addrs, err := l.dnsResolver.LookupSRV(ctx, "sip", proto, hostname)
	if err != nil {
		return fmt.Errorf("fail to lookup SRV for %q: %w", hostname, err)
	}

	log.Debug("SRV resolved", "addrs", addrs)
	record := addrs[0]

	ips, err := l.dnsResolver.LookupIP(ctx, "ip", record.Target)
	if err != nil {
		return err
	}

	log.Debug("SRV resolved IPS", "ips", ips, "target", record.Target)
	addr.IP = ips[0]
	addr.Port = int(record.Port)

	if addr.IP == nil {
		return fmt.Errorf("SRV resolving failed for %q", record.Target)
	}

	return nil
}

// GetConnection gets existing or creates new connection based on addr
func (l *TransportLayer) GetConnection(network, addr string) (Connection, error) {
	network = NetworkToLower(network)
	return l.getConnection(network, addr)
}

func (l *TransportLayer) getConnection(network, addr string) (Connection, error) {
	transport, ok := l.transports[network]
	if !ok {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	l.log.Debug("getting connection", "network", network, "addr", addr)
	c := transport.GetConnection(addr)
	if c == nil {
		return nil, fmt.Errorf("connection %q does not exist", addr)
	}

	return c, nil
}

func (l *TransportLayer) Close() error {
	l.log.Debug("Layer is closing")
	var werr error
	for _, t := range l.transports {
		if err := t.Close(); err != nil {
			werr = errors.Join(werr, err)
		}
	}
	if werr != nil {
		l.log.Debug("Layer closed with error", "error", werr)
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
		return ASCIIToLower(network)
	}
}

// NetworkToUpper is faster function converting udp, tcp to UDP, tcp
func NetworkToUpper(network string) string {
	// Switch is faster then lower
	switch network {
	case "udp":
		return "UDP"
	case "tcp":
		return "TCP"
	case "tls":
		return "TLS"
	case "ws":
		return "WS"
	case "wss":
		return "WSS"
	default:
		return ASCIIToUpper(network)
	}
}
