package sip

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
)

var ()

// TLS transport implementation
type transportTLS struct {
	*transportTCP

	// rootPool *x509.CertPool
	tlsConf   *tls.Config
	tlsClient func(conn net.Conn, hostname string) *tls.Conn
}

// newTLSTransport needs dialTLSConf for creating connections when dialing
// tls.Config must not be nil
func newTLSTransport(par *Parser, dialTLSConf *tls.Config, logger *slog.Logger) *transportTLS {
	tcptrans := newTCPTransport(par, logger)
	p := &transportTLS{
		transportTCP: tcptrans,
	}

	// p.rootPool = roots
	p.tlsConf = dialTLSConf
	p.tlsClient = func(conn net.Conn, hostname string) *tls.Conn {
		config := dialTLSConf

		if config.ServerName == "" {
			config = config.Clone()
			config.ServerName = hostname
		}
		return tls.Client(conn, config)
	}
	p.log = logger.With("caller", "transport<TLS>", "transport", "tls")
	return p
}

func (t *transportTLS) String() string {
	return "transport<TLS>"
}

func (*transportTLS) Network() string {
	return TransportTLS
}

// CreateConnection creates TLS connection for TCP transport
func (t *transportTLS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	hostname := raddr.Hostname
	if hostname == "" {
		hostname = raddr.IP.String()
	}

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

	netDialer := &net.Dialer{
		LocalAddr: tladdr,
	}

	addr := traddr.String()
	t.log.Debug("Dialing new connection", "raddr", addr)
	// No resolving should happen here
	conn, err := netDialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial TCP error: %w", err)
	}

	tlsConn := t.tlsClient(conn, hostname)

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("TLS handshake error: %w", err)
	}

	c := t.initConnection(tlsConn, addr, handler)
	c.Ref(1)
	return c, nil
}
