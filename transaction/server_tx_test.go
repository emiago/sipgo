package transaction

import (
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/livekit/sipgo/sip"
)

// refConn counts references like a pooled transport connection.
type refConn struct {
	net.Conn
	ref int
}

func (c *refConn) WriteMsg(sip.Message) error { return nil }
func (c *refConn) Ref(i int) int              { c.ref += i; return c.ref }
func (c *refConn) TryClose() (int, error)     { c.ref--; return c.ref, nil }
func (c *refConn) Close() error               { return nil }

// A server transaction releases the connection reference it was given, once,
// however many times it is terminated.
func TestServerTxReleasesConnection(t *testing.T) {
	conn := &refConn{ref: 2}
	req := sip.NewRequest(sip.OPTIONS, sip.Uri{Scheme: "sip", Host: "10.2.2.2", Port: 5060})
	req.SetTransport("TCP")

	tx := NewServerTx("key", req, conn, slog.Default())
	tx.OnTerminate(func(string) {})
	require.NoError(t, tx.Init())

	tx.Terminate()
	tx.Terminate()
	require.Equal(t, 1, conn.ref)
}
