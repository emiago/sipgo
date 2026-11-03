package sip

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedWriteConn struct {
	fakes.TCPConn
	n      int
	err    error
	closed bool
}

func newFailedWriteConn(n int, err error) *failedWriteConn {
	return &failedWriteConn{
		TCPConn: fakes.TCPConn{
			LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
			RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5061},
			Reader: bytes.NewReader(nil),
		},
		n:   n,
		err: err,
	}
}

func (c *failedWriteConn) Write([]byte) (int, error) {
	return c.n, c.err
}

func (c *failedWriteConn) Close() error {
	c.closed = true
	return nil
}

func TestTCPWriteMsgFailureEvictsConnection(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, network := range []string{"tcp", "tls"} {
		for _, test := range []struct {
			name string
			n    int
			err  error
		}{
			{"error", 0, writeErr},
			{"partial_error", 1, writeErr},
			{"short_write", 1, nil},
			{"zero_write", 0, nil},
		} {
			t.Run(network+"/"+test.name, func(t *testing.T) {
				layer := NewTransportLayer(nil, NewParser(), nil)
				t.Cleanup(func() { _ = layer.Close() })
				transport := layer.tcp
				if network == "tls" {
					transport = layer.tls.TransportTCP
				}
				socket := newFailedWriteConn(test.n, test.err)
				conn := transport.newConnection(socket, 1+TransportIdleConnection)
				local, remote := socket.LocalAddr().String(), socket.RemoteAddr().String()
				transport.pool.Add(local, conn)
				transport.pool.Add(remote, conn)

				err := layer.WriteMsgTo(NewResponse(200, "OK"), remote, network)
				require.Error(t, err)
				if test.err != nil {
					require.ErrorIs(t, err, test.err)
				} else {
					require.ErrorIs(t, err, io.ErrShortWrite)
				}
				assert.Nil(t, transport.pool.getUnref(remote), "failed connection still pooled at remote address")
				assert.Nil(t, transport.pool.getUnref(local), "failed connection still pooled at local address")
				assert.True(t, socket.closed, "failed connection still open")
			})
		}
	}
}

func TestTCPWriteMsgFailureCleanupKeepsReplacement(t *testing.T) {
	for _, network := range []string{"tcp", "tls"} {
		t.Run(network, func(t *testing.T) {
			layer := NewTransportLayer(nil, NewParser(), nil)
			t.Cleanup(func() { _ = layer.Close() })
			transport := layer.tcp
			if network == "tls" {
				transport = layer.tls.TransportTCP
			}
			socket := newFailedWriteConn(0, errors.New("write failed"))
			old := transport.newConnection(socket, 1+TransportIdleConnection)
			local, remote := socket.LocalAddr().String(), socket.RemoteAddr().String()
			transport.pool.Add(local, old)
			transport.pool.Add(remote, old)

			require.Error(t, layer.WriteMsgTo(NewResponse(200, "OK"), remote, network))
			require.Zero(t, transport.pool.Size())
			require.True(t, socket.closed)

			nextSocket := newFailedWriteConn(0, nil)
			next := transport.newConnection(nextSocket, 1+TransportIdleConnection)
			transport.pool.Add(local, next)
			transport.pool.Add(remote, next)

			// Run the reader's EOF cleanup only after both addresses are replaced.
			transport.readConnection(old, local, remote, nil)
			require.Same(t, next, transport.pool.getUnref(local))
			require.Same(t, next, transport.pool.getUnref(remote))
			require.False(t, nextSocket.closed)
		})
	}
}
