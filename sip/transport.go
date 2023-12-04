package sip

import (
	"context"
	"net"
	"strconv"
)

var (
	SIPTrace bool

	// IdleConnection will keep connections idle even after transaction terminate
	// -1 	- single response or request will close
	// 0 	- close connection immediatelly after transaction terminate
	// 1 	- keep connection idle after transaction termination
	IdleConnection int = 1
)

const (
	// Transport for different sip messages. GO uses lowercase, but for message parsing, we should
	// use this constants for setting message Transport
	TransportUDP = "UDP"
	TransportTCP = "TCP"
	TransportTLS = "TLS"
	TransportWS  = "WS"
	TransportWSS = "WSS"

	transportBufferSize uint16 = 65535

	// TransportFixedLengthMessage sets message size limit for parsing and avoids stream parsing
	TransportFixedLengthMessage uint16 = 0
)

// Protocol implements network specific features.
type Transport interface {
	Network() string

	// GetConnection returns connection from transport
	// addr must be resolved to IP:port
	GetConnection(addr string) (Connection, error)
	CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error)
	String() string
	Close() error
}

type Addr struct {
	IP   net.IP // Must be in IP format
	Port int
}

func (a *Addr) String() string {
	if a.IP == nil {
		return net.JoinHostPort("", strconv.Itoa(a.Port))
	}

	return net.JoinHostPort(a.IP.String(), strconv.Itoa(a.Port))
}
