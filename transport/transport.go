package transport

import (
	"net"
	"strconv"

	"github.com/emiago/sipgo/sip"
)

var (
	SIPDebug bool

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
	GetConnection(addr string) (Connection, error)
	CreateConnection(laddr Addr, raddr Addr, handler sip.MessageHandler) (Connection, error)
	String() string
	Close() error
}

type Addr struct {
	IP   net.IP // Must be in IP format
	Port int
}

func (a *Addr) String() string {
	return net.JoinHostPort(a.IP.String(), strconv.Itoa(a.Port))
}
