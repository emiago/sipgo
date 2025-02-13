package sip

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"
)

// TLS transport implementation
type transportWSS struct {
	*transportWS

	// rootPool *x509.CertPool
}

func (t *transportWSS) init(par *Parser, dialTLSConf *tls.Config) {
	t.transportWS.init(par)
	t.transportWS.transport = TransportWSS
	t.dialer.TLSConfig = dialTLSConf

	t.dialer.TLSClient = func(conn net.Conn, hostname string) net.Conn {
		// This is just extracted from tls dialer code
		config := dialTLSConf

		if config.ServerName == "" {
			config = config.Clone()
			config.ServerName = hostname
		}
		return tls.Client(conn, config)
	}

	if t.log == nil {
		t.log = slog.Default()
	}
}

func (t *transportWSS) String() string {
	return "transport<WSS>"
}

// CreateConnection creates WSS connection for TCP transport
// TODO Make this consisten with TCP
func (t *transportWSS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	log := t.log

	// Must have IP resolved
	if raddr.IP == nil {
		return nil, fmt.Errorf("remote address IP not resolved")
	}

	// We need to distict IPAddr vs address with hostname
	// Hostname must be passed for TLS if provided due to certificates check
	hostname := raddr.Hostname
	if hostname == "" {
		hostname = raddr.IP.String()
	}
	addr := net.JoinHostPort(hostname, strconv.Itoa(raddr.Port))

	// USe default unless local address is set
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

	// Make sure we have port set
	if traddr.Port == 0 {
		traddr.Port = 443
	}

	netDialer := &net.Dialer{
		LocalAddr: tladdr,
	}

	log.Debug("Dialing new connection", "raddr", traddr.String())
	conn, err := netDialer.DialContext(ctx, "tcp", traddr.String())
	if err != nil {
		return nil, fmt.Errorf("dial TCP error: %w", err)
	}

	log.Debug("Setuping TLS connection", "hostname", hostname)
	tlsConn := t.dialer.TLSClient(conn, hostname)

	u, err := url.ParseRequestURI("wss://" + addr)
	if err != nil {
		return nil, fmt.Errorf("parse request wss uri failed: %w", err)
	}

	// Check ctx deadline
	// TODO handle cancelation?
	if deadline, ok := ctx.Deadline(); ok {
		tlsConn.SetDeadline(deadline)
		defer tlsConn.SetDeadline(time.Time{})
	}

	_, _, err = t.dialer.Upgrade(tlsConn, u)
	if err != nil {
		return nil, fmt.Errorf("failed to upgrade: %w", err)
	}

	ipAddr := traddr.String()
	c := t.initConnection(tlsConn, ipAddr, true, handler)
	c.Ref(1)
	return c, nil
}
