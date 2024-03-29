package sip

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/rs/zerolog/log"
)

var ()

// TLS transport implementation
type transportTLS struct {
	*transportTCP

	// rootPool *x509.CertPool
	tlsConf *tls.Config
}

// newTLSTransport needs dialTLSConf for creating connections when dialing
func newTLSTransport(par *Parser, dialTLSConf *tls.Config) *transportTLS {
	tcptrans := newTCPTransport(par)
	tcptrans.transport = TransportTLS //Override transport
	p := &transportTLS{
		transportTCP: tcptrans,
	}

	// p.rootPool = roots
	p.tlsConf = dialTLSConf
	p.log = log.Logger.With().Str("caller", "transport<TLS>").Logger()
	return p
}

func (t *transportTLS) String() string {
	return "transport<TLS>"
}

// CreateConnection creates TLS connection for TCP transport
func (t *transportTLS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
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

func (t *transportTLS) createConnection(ctx context.Context, laddr *net.TCPAddr, raddr *net.TCPAddr, handler MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	//TODO does this need to be each config
	// SHould we make copy of rootPool?
	// There is Clone of config

	dialer := tls.Dialer{
		NetDialer: &net.Dialer{
			LocalAddr: laddr,
		},
		Config: t.tlsConf,
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr, handler)
	c.Ref(1)
	return c, nil
}
