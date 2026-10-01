package sip

import (
	"bytes"
	"net"
	"testing"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

type poolCleanupStream struct {
	fakes.TCPConn
	closes int
}

func (c *poolCleanupStream) Close() error {
	c.closes++
	return nil
}

func newPoolCleanupStream() *poolCleanupStream {
	return &poolCleanupStream{TCPConn: fakes.TCPConn{
		LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
		Reader: bytes.NewReader(nil),
	}}
}

func TestConnectionPoolCloseAndDeleteSuccessor(t *testing.T) {
	// Exercise both the last-reference close and the existing force-close path.
	for _, refs := range []int{1, 2} {
		pool := newConnectionPool()
		oldSocket, newSocket := newPoolCleanupStream(), newPoolCleanupStream()
		old := &TCPConnection{Conn: oldSocket, refcount: refs}
		next := &TCPConnection{Conn: newSocket, refcount: 1}
		pool.Add("remote", old)
		pool.Add("remote", next)

		require.NoError(t, pool.CloseAndDelete(old, "remote"))
		require.Same(t, next, pool.getUnref("remote"))
		require.Equal(t, 1, oldSocket.closes)
		require.Zero(t, newSocket.closes)
		require.Equal(t, 1, next.Ref(0))

		require.NoError(t, pool.CloseAndDelete(next, "remote"))
		require.Zero(t, pool.Size())
		require.Equal(t, 1, newSocket.closes)
	}
}

func TestStreamReaderCleanupPreservesSuccessor(t *testing.T) {
	for _, network := range []string{"tcp", "tls", "ws", "wss"} {
		for _, replace := range []bool{false, true} {
			name := network + "/sole"
			if replace {
				name = network + "/successor"
			}
			t.Run(name, func(t *testing.T) {
				layer := NewTransportLayer(nil, NewParser(), nil)
				pool := layer.Pool(network)
				oldSocket, newSocket := newPoolCleanupStream(), newPoolCleanupStream()
				var old, next Connection
				var read func()
				if network == "tcp" || network == "tls" {
					transport := layer.tcp
					if network == "tls" {
						transport = layer.tls.TransportTCP
					}
					conn := transport.newConnection(oldSocket, 1)
					old, next = conn, transport.newConnection(newSocket, 1)
					read = func() { transport.readConnection(conn, "local", "remote", nil) }
				} else {
					transport := layer.ws
					if network == "wss" {
						transport = layer.wss.TransportWS
					}
					conn := transport.newConnection(oldSocket, 1, false)
					old, next = conn, transport.newConnection(newSocket, 1, false)
					read = func() { transport.readConnection(conn, "local", "remote", nil) }
				}
				pool.Add("local", old)
				pool.Add("remote", old)
				if replace {
					pool.Add("local", next)
					pool.Add("remote", next)
				}

				// EOF runs the real reader's deferred cleanup after replacement.
				read()
				require.Equal(t, 1, oldSocket.closes)
				require.Zero(t, newSocket.closes)
				if replace {
					require.Same(t, next, pool.getUnref("local"))
					require.Same(t, next, pool.getUnref("remote"))
				} else {
					require.Zero(t, pool.Size())
				}
			})
		}
	}
}

type poolCleanupPacket struct {
	fakes.UDPConn
	closes int
}

func (c *poolCleanupPacket) Close() error {
	c.closes++
	return nil
}

func TestUDPReaderCleanupPreservesSuccessor(t *testing.T) {
	for _, listener := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			name := "dialed"
			if listener {
				name = "listener"
			}
			if replace {
				name += "/successor"
			} else {
				name += "/sole"
			}
			t.Run(name, func(t *testing.T) {
				layer := NewTransportLayer(nil, NewParser(), nil)
				pool := layer.udp.pool
				socket := &poolCleanupPacket{UDPConn: fakes.UDPConn{
					LAddr:  net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
					RAddr:  net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5061},
					Reader: bytes.NewReader(testRawOptions("pool-cleanup")),
				}}
				old := &UDPConnection{PacketConn: socket, Listener: listener, refcount: 1}
				next := &UDPConnection{refcount: 1}
				local, peer := socket.LocalAddr().String(), socket.RemoteAddr().String()
				pool.Add(local, old)
				if !listener {
					pool.Add("dialed-remote", old)
				}
				handled := false
				handler := func(Message) {
					handled = true
					require.Same(t, old, pool.getUnref(peer))
					if replace {
						pool.Add(local, next)
						pool.Add(peer, next)
						if !listener {
							pool.Add("dialed-remote", next)
						}
					}
				}
				if listener {
					layer.udp.readListenerConnection(old, local, handler)
				} else {
					layer.udp.readUDPConnection(old, "dialed-remote", local, handler)
				}
				require.True(t, handled)
				if listener {
					require.Zero(t, socket.closes, "caller owns the listener socket")
				} else {
					require.Equal(t, 1, socket.closes)
				}
				if replace {
					require.Same(t, next, pool.getUnref(local))
					require.Same(t, next, pool.getUnref(peer))
					if !listener {
						require.Same(t, next, pool.getUnref("dialed-remote"))
					}
					require.Equal(t, 1, next.Ref(0))
				} else {
					require.Zero(t, pool.Size())
				}
			})
		}
	}
}
