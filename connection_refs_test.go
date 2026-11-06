package sipgo

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/livekit/sipgo/sip"
	"github.com/livekit/sipgo/transport"
)

// doubleReleases counts the transports' warnings that a connection was released
// more times than it was referenced.
type doubleReleases struct {
	slog.Handler
	n atomic.Int32
}

func (h *doubleReleases) Handle(_ context.Context, r slog.Record) error {
	if strings.HasSuffix(r.Message, "ref went negative") {
		h.n.Add(1)
	}
	return nil
}

func (h *doubleReleases) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *doubleReleases) WithGroup(string) slog.Handler      { return h }

// refSide is one end of a connection: a user agent that sends requests through
// its client and answers them through its server.
type refSide struct {
	ua  *UserAgent
	cli *Client
	// gate decides each request the server receives: true answers it with 200,
	// false drops it. Until then its server transaction stays open.
	gate chan bool
}

func newRefSide(t *testing.T, log *slog.Logger) *refSide {
	t.Helper()
	ua, err := NewUA(
		WithUserAgentIP(net.IPv4(127, 0, 0, 1)),
		WithUserAgentLogger(log),
		WithUserAgenTLSConfig(&tls.Config{InsecureSkipVerify: true}),
	)
	require.NoError(t, err)
	srv, err := NewServer(ua)
	require.NoError(t, err)
	cli, err := NewClient(ua)
	require.NoError(t, err)
	s := &refSide{ua: ua, cli: cli, gate: make(chan bool, 4)}
	srv.OnOptions(func(_ *slog.Logger, req *sip.Request, tx sip.ServerTransaction) {
		if <-s.gate {
			_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
		}
	})
	t.Cleanup(func() {
		close(s.gate)
		ua.Close()
	})
	return s
}

func (s *refSide) options(network string, to net.Addr) *sip.Request {
	addr := to.(*net.TCPAddr)
	req := sip.NewRequest(sip.OPTIONS, sip.Uri{Scheme: "sip", Host: addr.IP.String(), Port: addr.Port})
	req.SetTransport(strings.ToUpper(network))
	return req
}

// refs is the number of references held on the connection to addr, not
// counting the one taken to look.
func (s *refSide) refs(t *testing.T, network string, addr net.Addr) int {
	t.Helper()
	c, err := s.ua.TransportLayer().GetConnection(network, addr.String())
	require.NoError(t, err)
	n := c.Ref(0) - 1
	_, err = c.TryClose()
	require.NoError(t, err)
	return n
}

type refPair struct {
	network string
	warns   *doubleReleases
	// acpt served the listener and accepted the connection; dial dialed it.
	acpt, dial         *refSide
	acptAddr, dialAddr net.Addr
}

func listenFor(t *testing.T, network string, srv *transport.Layer) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	switch network {
	case "tcp":
		go srv.ServeTCP(l)
	case "tls":
		l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}})
		go srv.ServeTLS(l)
	case "ws":
		go srv.ServeWS(l)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// newRefPair connects two sides. With dialTx the dial is a transaction left
// open, so the dialer's reference is still held; otherwise the dial is a
// request sent outside a transaction, whose reference is already released.
// It returns the open transaction, if any.
func newRefPair(t *testing.T, network string, dialTx bool) (*refPair, sip.ClientTransaction) {
	t.Helper()
	warns := &doubleReleases{Handler: slog.NewTextHandler(io.Discard, nil)}
	log := slog.New(warns)
	p := &refPair{network: network, warns: warns, acpt: newRefSide(t, log), dial: newRefSide(t, log)}
	p.acptAddr = listenFor(t, network, p.acpt.ua.TransportLayer()).Addr()

	var tx sip.ClientTransaction
	req := p.dial.options(network, p.acptAddr)
	if dialTx {
		var err error
		tx, err = p.dial.cli.TransactionRequest(req)
		require.NoError(t, err)
	} else {
		p.acpt.gate <- true
		require.NoError(t, p.dial.cli.WriteRequest(req))
	}

	c, err := p.dial.ua.TransportLayer().GetConnection(network, p.acptAddr.String())
	require.NoError(t, err)
	p.dialAddr = c.LocalAddr()
	_, err = c.TryClose()
	require.NoError(t, err)
	// The dialer can finish its handshake before the accepting side pools the
	// connection, and a request sent back before then would dial instead.
	require.Eventually(t, func() bool {
		c, err := p.acpt.ua.TransportLayer().GetConnection(network, p.dialAddr.String())
		if err != nil {
			return false
		}
		_, _ = c.TryClose()
		return true
	}, 2*time.Second, 5*time.Millisecond)
	return p, tx
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func waitRefs(t *testing.T, s *refSide, network string, addr net.Addr, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return s.refs(t, network, addr) == want },
		2*time.Second, 5*time.Millisecond, "want %d references", want)
}

// Every reference to a connection has one owner: the pool, or a transaction
// using the connection. The side under test sends txs requests and the other
// side holds its answers, so both ends carry 1 (the pool's) + txs. However the
// connection ends, every count gets back to where it should, and nothing
// releases a reference twice.
//
// A dialed connection with 0 transactions was dialed by a request sent outside a
// transaction. With 1 or more, the first is the transaction that dialed it.
func TestConnectionReferences(t *testing.T) {
	for _, network := range []string{"tcp", "tls", "ws"} {
		for _, side := range []string{"accepted", "dialed"} {
			for txs := 0; txs <= 2; txs++ {
				// open connects a pair and leaves txs transactions open from the
				// side under test. It returns the sender with the address of the
				// connection from its end, the responder with the same, and the
				// transactions.
				open := func(t *testing.T) (p *refPair, from *refSide, fromPeer net.Addr, to *refSide, toPeer net.Addr, open []sip.ClientTransaction) {
					p, tx := newRefPair(t, network, side == "dialed" && txs > 0)
					from, fromPeer, to, toPeer = p.dial, p.acptAddr, p.acpt, p.dialAddr
					if side == "accepted" {
						from, fromPeer, to, toPeer = p.acpt, p.dialAddr, p.dial, p.acptAddr
					}
					if tx != nil {
						open = append(open, tx)
					}
					for len(open) < txs {
						tx, err := from.cli.TransactionRequest(from.options(network, fromPeer))
						require.NoError(t, err)
						open = append(open, tx)
					}
					waitRefs(t, from, network, fromPeer, 1+txs)
					waitRefs(t, to, network, toPeer, 1+txs)
					return p, from, fromPeer, to, toPeer, open
				}

				name := fmt.Sprintf("%s/%s/%dtx", network, side, txs)

				t.Run(name+"/answered", func(t *testing.T) {
					p, from, fromPeer, to, toPeer, txs := open(t)
					// Handlers take from the gate in any order, so open it for all of
					// them before waiting on any one transaction.
					for range txs {
						to.gate <- true
					}
					for _, tx := range txs {
						res := <-tx.Responses()
						require.NotNil(t, res, "transaction ended without a response: %v", tx.Err())
						require.Equal(t, sip.StatusCode(200), res.StatusCode)
						<-tx.Done()
					}
					// Only the pools' references are left, and they keep the
					// connection open for the next request.
					waitRefs(t, from, network, fromPeer, 1)
					waitRefs(t, to, network, toPeer, 1)

					tx, err := from.cli.TransactionRequest(from.options(network, fromPeer))
					require.NoError(t, err)
					to.gate <- true
					res := <-tx.Responses()
					require.NotNil(t, res, "transaction ended without a response: %v", tx.Err())
					require.Equal(t, sip.StatusCode(200), res.StatusCode)
					require.Zero(t, p.warns.n.Load())
				})

				t.Run(name+"/peer_closed", func(t *testing.T) {
					p, from, fromPeer, to, toPeer, txs := open(t)
					// Hold a reference from the side under test to watch the count
					// after the pool lets go.
					watched, err := from.ua.TransportLayer().GetConnection(network, fromPeer.String())
					require.NoError(t, err)

					peer, err := to.ua.TransportLayer().GetConnection(network, toPeer.String())
					require.NoError(t, err)
					require.NoError(t, peer.Close())
					_, err = peer.TryClose()
					require.NoError(t, err)

					require.Eventually(t, func() bool {
						_, err := from.ua.TransportLayer().GetConnection(network, fromPeer.String())
						return err != nil
					}, 2*time.Second, 5*time.Millisecond)

					for range txs {
						to.gate <- false
					}
					for _, tx := range txs {
						tx.Terminate()
						<-tx.Done()
					}
					ref, err := watched.TryClose()
					require.NoError(t, err)
					require.Zero(t, ref)
					require.Zero(t, p.warns.n.Load())
				})
			}
		}
	}
}
