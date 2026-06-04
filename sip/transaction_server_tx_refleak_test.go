package sip

import (
	"log/slog"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type refCountConn struct {
	mu        sync.Mutex
	ref       int
	tryCloses int
}

func (c *refCountConn) LocalAddr() net.Addr        { return nil }
func (c *refCountConn) WriteMsg(msg Message) error { return nil }
func (c *refCountConn) Close() error               { return nil }
func (c *refCountConn) Ref(i int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ref += i
	return c.ref
}
func (c *refCountConn) TryClose() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tryCloses++
	c.ref--
	return c.ref, nil
}

func TestServerTxReleasesConnectionRefOnTerminate(t *testing.T) {
	req := testCreateRequest(t, "OPTIONS", "sip:example.com", "UDP", "127.0.0.1:5060")

	conn := &refCountConn{}
	conn.Ref(1) // the reference serverRequestConnection takes before NewServerTx

	tx := NewServerTx("key", req, conn, slog.Default())
	require.NoError(t, tx.Init())

	tx.Terminate()
	<-tx.Done()

	require.Equal(t, 1, conn.tryCloses, "Terminate must TryClose the connection exactly once")
	require.Equal(t, 0, conn.ref, "connection reference must return to zero")
}
