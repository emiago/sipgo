package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"time"

	sipgo "github.com/emiago/sipgo/sip"

	"github.com/livekit/sipgo/sip"
)

// TLS transport implementation
type WSSTransport struct {
	*WSTransport

	// rootPool *x509.CertPool
}

// NewWSSTransport needs dialTLSConf for creating connections when dialing
func NewWSSTransport(log *slog.Logger, par *sipgo.Parser, dialTLSConf *tls.Config) *WSSTransport {
	tcptrans := NewWSTransport(log, par)
	tcptrans.transport = TransportWSS
	// Set our TLS config
	p := &WSSTransport{
		WSTransport: tcptrans,
	}

	p.dialer.TLSConfig = dialTLSConf

	// p.tlsConf = dialTLSConf
	p.log = log.With("caller", "transport<WSS>")
	return p
}

func (t *WSSTransport) String() string {
	return "transport<WSS>"
}

// CreateConnection creates WSS connection for TCP transport
// TODO Make this consisten with TCP
func (t *WSSTransport) CreateConnection(laddr Addr, host string, raddr Addr, handler sip.MessageHandler) (Connection, error) {
	// raddr, err := net.ResolveTCPAddr("tcp", addr)
	// if err != nil {
	// 	return nil, err
	// }

	var tladdr *net.TCPAddr = nil
	if laddr.IP != nil {
		tladdr = &net.TCPAddr{
			IP:   laddr.IP,
			Port: laddr.Port,
		}
	}

	traddr := &net.TCPAddr{
		IP:   raddr.IP,
		Port: raddr.Port,
	}

	return t.createConnection(tladdr, traddr, handler)
}

func (t *WSSTransport) createConnection(laddr *net.TCPAddr, raddr *net.TCPAddr, handler sip.MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug("Dialing new connection", "raddr", addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// How to pass local interface

	conn, _, _, err := t.dialer.Dial(ctx, "wss://"+addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr, true, 2, handler)
	return c, nil
}
