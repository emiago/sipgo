package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/emiago/sipgo/sip"
	"github.com/gobwas/ws"

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
	p := &WSSTransport{
		WSTransport: tcptrans,
	}

	// TODO should have single or multiple dialers
	ws.DefaultDialer.TLSConfig = dialTLSConf

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

	conn, _, _, err := ws.Dial(context.TODO(), "wss://"+addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr, handler)
	return c, nil
}
