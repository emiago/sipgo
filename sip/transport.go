package sip

import (
	"context"
	"net"
	"strconv"
)

var (

	// TransportIdleConnection will keep connections idle even after transaction terminate
	// -1 	- single response or request will close
	// 0 	- close connection immediatelly after transaction terminate
	// 1 	- keep connection idle after transaction termination
	TransportIdleConnection int = 1

	// TransportBufferReadSize sets this buffer size to use on reading SIP messages.
	TransportBufferReadSize uint16 = 32768
)

const (
	DefaultProtocol = "UDP"

	DefaultUdpPort int = 5060
	DefaultTcpPort int = 5060
	DefaultTlsPort int = 5061
	DefaultWsPort  int = 80
	DefaultWssPort int = 443
)

// Protocol implements network specific features.
type transport interface {
	// GetConnection returns connection from transport
	// addr must be resolved to IP:port
	GetConnection(addr string) Connection
	CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error)
	Close() error
}

// DefaultPort returns transport default port by network.
func DefaultPort(transport string) int {
	switch ASCIIToLower(transport) {
	case "tls":
		return DefaultTlsPort
	case "tcp":
		return DefaultTcpPort
	case "udp":
		return DefaultUdpPort
	case "ws":
		return DefaultWsPort
	case "wss":
		return DefaultWssPort
	default:
		return DefaultTcpPort
	}
}

type Addr struct {
	IP       net.IP // Must be in IP format
	Port     int
	Hostname string // Original hostname before resolved to IP
	Zone     string
}

func (a *Addr) String() string {
	if a.IP == nil {
		return net.JoinHostPort(a.Hostname, strconv.Itoa(a.Port))
	}

	return net.JoinHostPort(a.IP.String(), strconv.Itoa(a.Port))
}

func (a *Addr) Copy(d *Addr) {
	d.Hostname = a.Hostname
	d.Port = a.Port
	if a.IP != nil {
		d.IP = make(net.IP, len(a.IP))
		copy(d.IP, a.IP)
	}
}

func (a *Addr) parseAddr(addr string) error {
	host, port, err := ParseAddr(addr)
	a.IP = net.ParseIP(host)
	a.Port = port
	a.Hostname = host
	return err
}

func ParseAddr(addr string) (host string, port int, err error) {
	host, pstr, err := net.SplitHostPort(addr)
	if err != nil {
		return host, port, err
	}

	// In case we are dealing with some named ports this should be called
	// net.LookupPort(network)

	port, err = strconv.Atoi(pstr)
	return host, port, err
}
