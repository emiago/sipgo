package sip

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gobwas/ws"
)

// tlsServerName is the name httptest's self-signed certificate is issued for.
// The transport is pointed at loopback by IP and verifies against this name, so
// the test exercises real certificate verification rather than switching it off.
const tlsServerName = "example.com"

// This test drives TransportWSS.CreateConnection itself, against a real TLS
// WebSocket server, and asserts what the pool ends up keyed under.
//
// Why it exists rather than another helper test: the pool and the alias list
// already have unit tests, and every one of them passes the arguments in by
// hand. That is precisely the shape of the bug they failed to catch. The rebase
// did not break the pool and it did not break wsConnectionAliases -- it broke
// the one line that hands them to each other (transport_wss.go:109), by
// transposing addSingleflight's laddr/raddr arguments and dropping the alias
// set. Reverting that single line reintroduces an undeliverable ConfBridge
// INVITE while every helper test stays green, because each of them supplies the
// correct wiring itself.
//
// So the assertion here is deliberately made on the pool AFTER a real dial,
// never on the helpers: it is the only way to catch a caller that composes
// correct pieces incorrectly.
//
// The failure this pins: keyed under our own local address, a lookup driven by
// the far end's address finds nothing, the registrar cannot route the INVITE
// back down the socket we opened, and -- because a WS client is not reachable at
// its contact address -- there is no second path. The call simply never happens,
// with no error anywhere.

// startTLSWebSocketServer brings up a TLS WebSocket endpoint on loopback that
// completes the upgrade and then holds the connection open. A real server is
// used because CreateConnection's registration only happens on a successful
// dial: a stubbed dialer would skip the very code path under test.
func startTLSWebSocketServer(t *testing.T) (ip net.IP, port int, roots *x509.CertPool) {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		// Drain and hold. The connection must stay up for the duration of the
		// test, otherwise the transport may reap it before we inspect the pool.
		go func() {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}()
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("splitting test server host/port: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing test server port: %v", err)
	}

	roots = x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	return net.ParseIP(host), p, roots
}

// newDialingWSSTransport builds a WSS transport that trusts exactly the test
// server's certificate. Verification stays on: the transport reaches loopback by
// IP while verifying the name the certificate was issued for, which is the same
// split the production dial relies on.
func newDialingWSSTransport(t *testing.T, roots *x509.CertPool) *TransportWSS {
	t.Helper()

	tran := &TransportWSS{TransportWS: &TransportWS{}}
	tran.init(NewParser(), &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	return tran
}

// TestCreateConnectionDoesNotPoolUnderLocalAddressOnly pins the transposition
// directly. The local address being present is harmless -- the pool registers it
// deliberately. The bug was that it was present INSTEAD of the remote one, so
// asserting the remote key exists is what discriminates.
func TestCreateConnectionDoesNotPoolUnderLocalAddressOnly(t *testing.T) {
	ip, port, roots := startTLSWebSocketServer(t)
	tran := newDialingWSSTransport(t, roots)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	raddr := Addr{IP: ip, Port: port, Hostname: tlsServerName}

	conn, err := tran.CreateConnection(ctx, Addr{}, raddr, func(msg Message) {})
	if err != nil {
		t.Fatalf("dialling the test WebSocket server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	remoteKey := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	pooled := tran.pool.Get(remoteKey)
	if pooled == nil {
		t.Fatalf("the socket is not pooled under the remote address %q; with the pool keyed on our "+
			"local address instead, every lookup the far end drives misses and the INVITE is "+
			"undeliverable", remoteKey)
	}

	// Pointer identity, not address equality: the remote key must resolve to the
	// very socket we opened, since that connection is the only path back to us.
	if pooled != conn {
		t.Errorf("the connection pooled under %q is not the one we opened (local %s vs %s)",
			remoteKey, pooled.LocalAddr(), conn.LocalAddr())
	}
}
