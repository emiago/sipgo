package sip

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// TLS transport implementation
type TransportWSS struct {
	*TransportWS
}

func (t *TransportWSS) init(par *Parser, dialTLSConf *tls.Config) {
	t.TransportWS.init(par)
	t.TransportWS.transport = "WSS"
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
		t.log = DefaultLogger()
	}
}

func (t *TransportWSS) String() string {
	return "transport<WSS>"
}

// CreateConnection creates WSS connection for TCP transport
// TODO Make this consisten with TCP
func (t *TransportWSS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	log := t.log

	// Must have IP resolved
	if raddr.IP == nil {
		return nil, fmt.Errorf("remote address IP not resolved")
	}

	conn, err := t.pool.addSingleflight(laddr, raddr, t.connectionReuse, func() (Connection, error) {
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

		c := &WSConnection{
			Conn:       tlsConn,
			refcount:   2 + TransportIdleConnection,
			clientSide: true,
		}
		go t.readConnection(c, c.LocalAddr().String(), c.RemoteAddr().String(), handler)
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	c := conn.(*WSConnection)
	return c, nil
}
