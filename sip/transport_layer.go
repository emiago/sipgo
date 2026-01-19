package sip

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"
)

var (
	tlsEmptyConf tls.Config

	// Errors
	ErrTransportNotSuported = errors.New("protocol not supported")

	// No need yet to expose
	errTransportConnectionDoesNotExists = errors.New("connection does not exists")
)

// TransportLayer implementation.
type TransportLayer struct {
	udp *TransportUDP
	tcp *TransportTCP
	tls *TransportTLS
	ws  *TransportWS
	wss *TransportWSS

	listenPorts   map[string][]int
	listenPortsMu sync.Mutex
	dnsResolver   *net.Resolver

	handlers []MessageHandler

	log *slog.Logger

	// connectionReuse will force connection reuse when passing request
	connectionReuse bool

	// dnsPreferSRV does always SRV lookup first
	dnsPreferSRV bool
	dnsPreferIP  int // 0 - no preference , 1 -ip4, 2 - ip6
}

type TransportLayerOption func(l *TransportLayer)

func WithTransportLayerLogger(logger *slog.Logger) TransportLayerOption {
	return func(l *TransportLayer) {
		if logger != nil {
			l.log = logger.With("caller", "TransportLayer")
		}
	}
}

func WithTransportLayerConnectionReuse(f bool) TransportLayerOption {
	return func(l *TransportLayer) {
		l.connectionReuse = f
	}
}

func WithTransportLayerDNSLookupSRV(preferSRV bool) TransportLayerOption {
	return func(l *TransportLayer) {
		l.dnsPreferSRV = preferSRV
	}
}

// TODO will be exposed
// withTransportLayerDNSLookupIP allows to set which ip4 or ip6 to prefer on resolve
// default is ip4
func withTransportLayerDNSLookupIP(preferIP string) TransportLayerOption {
	return func(l *TransportLayer) {
		switch preferIP {
		case "ip4":
			l.dnsPreferIP = 1
		case "ip6":
			l.dnsPreferIP = 2
		default:
			l.dnsPreferIP = 0
		}
	}
}

type TransportsConfig struct {
	UDP *TransportUDP
	TCP *TransportTCP
	TLS *TransportTLS
	WS  *TransportWS
	WSS *TransportWSS
}

func WithTransportLayerTransports(conf TransportsConfig) TransportLayerOption {
	return func(l *TransportLayer) {
		l.withTransports(conf)
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
		listenPorts:     make(map[string][]int),
		dnsResolver:     dnsResolver,
		connectionReuse: true,
		log:             DefaultLogger().With("caller", "TransportLayer"),
		dnsPreferIP:     1, // IPV4
	}

	for _, o := range option {
		o(l)
	}

	if tlsConfig == nil {
		// Use empty tls config
		tlsConfig = &tlsEmptyConf
	}

	// Create our default transports settings
	transports := TransportsConfig{
		UDP: &TransportUDP{
			log:             l.log.With("caller", "Transport<UDP>"),
			connectionReuse: l.connectionReuse,
		},
		TCP: &TransportTCP{
			log:             l.log.With("caller", "Transport<TCP>"),
			connectionReuse: l.connectionReuse,
		},
		TLS: &TransportTLS{
			TransportTCP: &TransportTCP{
				log:             l.log.With("caller", "Transport<TLS>"),
				connectionReuse: l.connectionReuse,
			},
		},
		WS: &TransportWS{
			log: l.log.With("caller", "Transport<WS>"),
		},
		// TODO. Using default dial tls, but it needs to configurable via client
		WSS: &TransportWSS{
			TransportWS: &TransportWS{
				log:             l.log.With("caller", "Transport<WSS>"),
				connectionReuse: l.connectionReuse,
			},
		},
	}

	l.withTransports(transports)

	l.udp.init(sipparser)
	l.tcp.init(sipparser)
	l.tls.init(sipparser, tlsConfig)
	l.ws.init(sipparser)
	l.wss.init(sipparser, tlsConfig)

	return l
}

func (l *TransportLayer) withTransports(conf TransportsConfig) {
	if conf.UDP != nil && l.udp == nil {
		l.udp = conf.UDP
	}
	if conf.TCP != nil && l.tcp == nil {
		l.tcp = conf.TCP
	}
	if conf.TLS != nil && l.tls == nil {
		l.tls = conf.TLS
	}
	if conf.WS != nil && l.ws == nil {
		l.ws = conf.WS
	}
	if conf.WSS != nil && l.wss == nil {
		l.wss = conf.WSS
	}
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
	transport := l.getTransport(network)
	if transport == nil {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	raddr := Addr{}
	if err := l.resolveRemoteAddr(ctx, network, req.Destination(), req.Recipient.Scheme, &raddr); err != nil {
		return nil, err
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

	// Clients need to be able to bind request to IP:port.
	// Via host and port may not be same, as client would use some advertised host:port if present
	// In case not present in VIA, transport will override with real connection and IP
	// This makes sure that alwasy valid Via Header is set.
	// Client must guarantee that Via Header is added and present in each request.

	// Should we use default transport ports here?
	laddr := req.Laddr
	req.raddr = raddr

	// This is probably client forcing host:port
	if laddr.IP != nil && laddr.Port > 0 {
		c = transport.GetConnection(laddr.String())
	} else if l.connectionReuse {
		addr := raddr.String()
		c = transport.GetConnection(addr)
	}

	if c == nil {
		if l.log.Enabled(ctx, slog.LevelDebug) {
			// printing laddr adds some execution
			l.log.Debug("Creating connection", "laddr", laddr.String(), "raddr", raddr.String(), "network", network)
		}
		c, err = transport.CreateConnection(ctx, laddr, raddr, l.handleMessage)
		if err != nil {
			return nil, err
		}
	}

	if err := l.overrideSentBy(c, viaHop); err != nil {
		return nil, err
	}

	return c, nil
}

// serverRequestConnection implements serving connection when response needs to be returned
// Based on: https://datatracker.ietf.org/doc/html/rfc3261#section-18.2.2
//
// NOTE: this normally should be called one time for request transaction
func (l *TransportLayer) serverRequestConnection(ctx context.Context, req *Request) (c Connection, err error) {
	network := NetworkToLower(req.Transport())
	transport := l.getTransport(network)
	if transport == nil {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	sourceAddr := req.MessageData.Source()
	if IsReliable(network) && sourceAddr != "" {
		// If the "sent-protocol" is a reliable transport protocol such as
		//  TCP or SCTP, or TLS over those, the response MUST be sent using
		//  the existing connection to the source of the original request

		// connection can be matched by source(remote) addr
		conn := transport.GetConnection(req.MessageData.Source())
		if conn != nil {
			return conn, nil
		}
	}

	viaHop := req.Via()
	if viaHop == nil {
		return nil, fmt.Errorf("no Via Header present")
	}

	// TODO: refactor bellow to remove duplicate code

	viaHost, viaPort := req.sourceViaHostPort()
	if sourceAddr != "" {
		// https://datatracker.ietf.org/doc/html/rfc3263#section-5
		// 		for unreliable transport protocols, to the source
		//    address of the request, and the port in the Via header field

		// Use source host and port
		sourceHost, sourcePort, err := ParseAddr(sourceAddr)
		if err != nil {
			return nil, err
		}
		// Use source address and via port
		raddr := Addr{
			IP:       net.ParseIP(sourceHost),
			Port:     viaPort,
			Hostname: sourceHost,
		}

		// https://datatracker.ietf.org/doc/html/rfc3581#section-4
		// If rport is set then we must use sourcePort instead
		if viaHop != nil && viaHop.Params != nil {
			if rport, ok := viaHop.Params.Get("rport"); ok && rport == "" {
				raddr.Port = sourcePort
			}
		}

		// Should never be case but for safety
		if raddr.Port == 0 {
			// Use default port for transport
			raddr.Port = DefaultPort(network)
		}

		/// Set request addr here which is used for response build and setting received and rport if needed
		req.raddr = raddr

		// Check by source
		if c := transport.GetConnection(sourceAddr); c != nil {
			return c, nil
		}
		if c := transport.GetConnection(raddr.String()); c != nil {
			return c, nil
		}

		// Fail with via host
		// NOTE: This can happen at any point when server sending response and we are not handling those cases.
		// Generally this may have issue with setups where connections are closed after each response
	}

	raddr := Addr{
		// IP:       net.ParseIP(uriNetIP(viaHost)),
	}

	if err := l.resolveRemoteAddr(ctx, network, uriNetIP(viaHost), req.Recipient.Scheme, &raddr); err != nil {
		return nil, err
	}

	// Set request remote address to be used for further responses
	// NOTE: for race reasons this should not be exposed
	req.raddr = raddr

	// Check by source
	if sourceAddr != "" {
		if c := transport.GetConnection(sourceAddr); c != nil {
			return c, nil
		}
	}

	// Check is there some connection to be reused
	addr := raddr.String()
	if c := transport.GetConnection(addr); c != nil {
		return c, nil
	}

	// Fail with new connection :(
	// What about local Addr? It has usage for client
	laddr := Addr{}
	if l.log.Enabled(ctx, slog.LevelDebug) {
		// printing laddr adds some execution
		l.log.Debug("Creating server connection", "laddr", laddr.String(), "raddr", raddr.String(), "network", network)
	}
	c, err = transport.CreateConnection(ctx, laddr, raddr, l.handleMessage)
	return c, err
}

func (l *TransportLayer) resolveRemoteAddr(ctx context.Context, network string, a string, sipScheme string, raddr *Addr) error {
	host, port, err := ParseAddr(a)
	if err != nil {
		return fmt.Errorf("parse address failed for %s: %w", a, err)
	}
	raddr.Hostname = host
	raddr.Port = port
	if raddr.Port == 0 {
		// Use default port for transport
		raddr.Port = DefaultPort(network)
	}

	netaddr, err := netip.ParseAddr(host)
	// dns srv lookup
	if err != nil || !netaddr.IsValid() {
		// 	// https://datatracker.ietf.org/doc/html/rfc3263#section-5
		// 	// NOTE: Full RFC may require we do this differentely
		// 	// Whe port exists AAAA is used otherwise it should go immediately by SRV
		// 	// In our case we do both by default

		if err := l.resolveAddr(ctx, network, host, sipScheme, raddr); err != nil {
			return err
		}

		// We have did this before. This fixes only UDP as it is connectionless protocol
		// Now this is fixed with extra raddr var, in order to avoid Destination changes done by user
		// req.SetDestination(raddr.String())
		return nil
	}

	ipBytes := netaddr.As16()
	raddr.IP = net.IP(ipBytes[:])
	return nil
}

func (l *TransportLayer) overrideSentBy(c Connection, viaHop *ViaHeader) error {
	if viaHop.Host != "" && viaHop.Port > 0 {
		// avoids underhood parsing
		return nil
	}

	// TODO: can we have non string LAddr to avoid parsing
	la := c.LocalAddr()
	laStr := la.String()

	host, port, err := ParseAddr(laStr)
	if err != nil {
		return fmt.Errorf("fail to parse local connection address network=%s addr=%s: %w", la.Network(), laStr, err)
	}

	// https://datatracker.ietf.org/doc/html/rfc3261#section-18
	// Before a request is sent, the client transport MUST insert a value of
	// the "sent-by" field into the Via header field.  This field contains
	// an IP address or host name, and port.  The usage of an FQDN is
	// RECOMMENDED.
	// We are overriding only if client did not set this
	if viaHop.Host == "" {
		viaHop.Host = host
	}

	if viaHop.Port == 0 {
		viaHop.Port = port
	}
	return nil
}

func (l *TransportLayer) resolveAddr(ctx context.Context, network string, host string, sipScheme string, addr *Addr) error {
	log := l.log
	defer func(start time.Time) {
		if dur := time.Since(start); dur > 50*time.Millisecond {
			l.log.Warn("DNS resolution is slow", "dur", dur)
		}
	}(time.Now())

	if l.dnsPreferSRV {
		err := l.resolveAddrSRV(ctx, network, host, sipScheme, addr)
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
	return l.resolveAddrSRV(ctx, network, host, sipScheme, addr)
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

	// Prefer IPV4
	if l.dnsPreferIP > 0 {
		checkIp := func(ip net.IP) bool {
			// This is only correct way to check is ipv4.
			if l.dnsPreferIP == 1 {
				return ip.To4() != nil
			}
			return ip.To4() == nil
		}

		for _, ip := range ips {
			if checkIp(ip.IP) {
				addr.IP = ip.IP
				addr.Zone = ip.Zone
				return nil
			}
		}
	}

	addr.IP = ips[0].IP
	addr.Zone = ips[0].Zone
	return nil
}

func (l *TransportLayer) resolveAddrSRV(ctx context.Context, network string, hostname string, sipScheme string, addr *Addr) error {
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

	log.Debug("Doing SRV lookup", "scheme", sipScheme, "proto", proto, "host", hostname)

	// The returned records are sorted by priority and randomized
	// by weight within a priority.
	_, addrs, err := l.dnsResolver.LookupSRV(ctx, sipScheme, proto, hostname)
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
	transport := l.getTransport(network)
	if transport == nil {
		return nil, fmt.Errorf("transport %s is not supported", network)
	}

	l.log.Debug("getting connection", "network", network, "addr", addr)
	c := transport.GetConnection(addr)
	if c == nil {
		return nil, errTransportConnectionDoesNotExists
	}

	return c, nil
}

func (l *TransportLayer) Close() error {
	l.log.Debug("Layer is closing")
	var werr error
	for _, t := range l.allTransports() {
		if t == nil {
			continue
		}
		if err := t.Close(); err != nil {
			werr = errors.Join(werr, err)
		}
	}
	if werr != nil {
		l.log.Debug("Layer closed with error", "error", werr)
	}
	return werr
}

func (l *TransportLayer) getTransport(network string) transport {
	switch network {
	case "udp":
		return l.udp
	case "tcp":
		return l.tcp
	case "tls":
		return l.tls
	case "ws":
		return l.ws
	case "wss":
		return l.wss
	}
	return nil
}

func (l *TransportLayer) allTransports() []transport {
	return []transport{l.udp, l.tcp, l.tls, l.ws, l.wss}
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
