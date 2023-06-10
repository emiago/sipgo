package transport

import (
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

	transportBufferSize uint16 = 65535
)

// Protocol implements network specific features.
type Transport interface {
	Network() string
	GetConnection(addr string) (Connection, error)
	CreateConnection(addr string, handler sip.MessageHandler) (Connection, error)
	String() string
	Close() error
}
