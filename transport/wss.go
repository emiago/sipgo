package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog/log"
)

// TLS transport implementation
type WSSTransport struct {
	*WSTransport

	// rootPool *x509.CertPool
}

// NewWSSTransport needs dialTLSConf for creating connections when dialing
func NewWSSTransport(par sip.Parser, dialTLSConf *tls.Config) *WSSTransport {
	tcptrans := NewWSTransport(par)
	tcptrans.transport = TransportWSS
	// Set our TLS config
	p := &WSSTransport{
		WSTransport: tcptrans,
	}

	p.dialer.TLSConfig = dialTLSConf

	// p.tlsConf = dialTLSConf
	p.log = log.Logger.With().Str("caller", "transport<WSS>").Logger()
	return p
}

func (t *WSSTransport) String() string {
	return "transport<WSS>"
}

// CreateConnection creates WSS connection for TCP transport
// TODO Make this consisten with TCP
func (t *WSSTransport) CreateConnection(addr string, handler sip.MessageHandler) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	return t.createConnection(raddr.String(), handler)
}

func (t *WSSTransport) createConnection(addr string, handler sip.MessageHandler) (Connection, error) {
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, _, err := t.dialer.Dial(ctx, "wss://"+addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr, true, handler)
	return c, nil
}
