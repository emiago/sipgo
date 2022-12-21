package transport

import (
	"net"

	"github.com/emiago/sipgo/sip"
)

var (
	SIPDebug bool
)

const (
	// Transport for different sip messages. GO uses lowercase, but for message parsing, we should
	// use this constants for setting message Transport
	TransportUDP = "UDP"
	TransportTCP = "TCP"
	TransportTLS = "TLS"
	TransportWS  = "WS"
)

// Protocol implements network specific features.
type Transport interface {
	Addr() string
	Network() string
	Serve(handler sip.MessageHandler) error
	ResolveAddr(addr string) (net.Addr, error)
	GetConnection(addr string) (Connection, error)
	CreateConnection(addr string) (Connection, error)
	String() string
	Close() error
}
