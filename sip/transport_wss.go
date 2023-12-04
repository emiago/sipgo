package sip

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/rs/zerolog/log"
)

// TLS transport implementation
type transportWSS struct {
	*transportWS

	// rootPool *x509.CertPool
}

// NewWSSTransport needs dialTLSConf for creating connections when dialing
func NewWSSTransport(par *Parser, dialTLSConf *tls.Config) *transportWSS {
	tcptrans := NewWSTransport(par)
	tcptrans.transport = TransportWSS
	// Set our TLS config
	p := &transportWSS{
		transportWS: tcptrans,
	}

	p.dialer.TLSConfig = dialTLSConf

	// p.tlsConf = dialTLSConf
	p.log = log.Logger.With().Str("caller", "transport<WSS>").Logger()
	return p
}

func (t *transportWSS) String() string {
	return "transport<WSS>"
}

// CreateConnection creates WSS connection for TCP transport
// TODO Make this consisten with TCP
func (t *transportWSS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
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

	return t.createConnection(ctx, tladdr, traddr, handler)
}

func (t *transportWSS) createConnection(ctx context.Context, laddr *net.TCPAddr, raddr *net.TCPAddr, handler MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	// How to pass local interface

	conn, _, _, err := t.dialer.Dial(ctx, "wss://"+addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr, true, handler)
	c.Ref(1)
	return c, nil
}
