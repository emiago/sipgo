package transport

import (
	"net"
	"strconv"
	"time"

	"github.com/livekit/sipgo/sip"
)

var (
	SIPDebug bool

	// IdleConnection will keep connections idle even after transaction terminate
	// -1 	- single response or request will close
	// 0 	- close connection immediatelly after transaction terminate
	// 1 	- keep connection idle after transaction termination
	//
	// TODO: only 1 keeps reference counts balanced, see ConnectionPool.CloseAndDelete.
	IdleConnection int = 1

	// TCPReadTimeout closes a TCP or TLS connection that receives nothing for
	// this long. CRLF keep alives count as a read, so this mostly catches a
	// peer that went away without a FIN or RST. 0 disables it.
	TCPReadTimeout = 5 * time.Minute

	// TCPWriteTimeout fails a write on a TCP or TLS connection that cannot
	// finish in this long, which drops the connection. 0 disables it.
	TCPWriteTimeout = 10 * time.Second
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
	CreateConnection(laddr Addr, host string, raddr Addr, handler sip.MessageHandler) (Connection, error)
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
