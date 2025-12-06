package sip

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransportLayerClosing(t *testing.T) {
	// NOTE it creates real network connection

	// TODO add other transports
	for _, tran := range []string{"UDP"} {
		t.Run(tran, func(t *testing.T) {
			tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
			req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
			req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

			conn, err := tp.ClientRequestConnection(context.TODO(), req)
			require.NoError(t, err)

			tp.Close()
			c := conn.(*UDPConnection)
			require.Error(t, c.Close(), "It is not closed already")
		})
	}
}

func TestTransportLayerClientConnectionReuse(t *testing.T) {
	// NOTE it creates real network connection
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	defer func() {
		require.Empty(t, tp.udp.pool.Size())
	}()
	defer func() {
		require.NoError(t, tp.Close())
	}()
	require.True(t, tp.connectionReuse)

	t.Run("Default", func(t *testing.T) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		require.Equal(t, conn, conn2)
	})

	t.Run("WithClientHostPort", func(t *testing.T) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "localhost", Port: 12345, Params: NewParams()})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "localhost", Port: 12345, Params: NewParams()})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)
		require.Equal(t, conn, conn2)

		// Now same destination but forcing port
		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 9876, Params: NewParams()})
		req.Laddr = testCreateAddr(t, "127.0.0.1:9876")
		conn3, err := tp.ClientRequestConnection(context.TODO(), req)

		require.NoError(t, err)
		require.NotEqual(t, conn, conn3)
	})

	testParallel := func(t *testing.T, transport string) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})
		req.SetTransport(transport)
		connections := sync.Map{}
		wg := sync.WaitGroup{}

		for i := range 10 {
			wg.Add(1)
			go func(req *Request) {
				defer wg.Done()
				conn, err := tp.ClientRequestConnection(context.TODO(), req)
				t.Log("Created connect", conn.LocalAddr().String())
				require.NoError(t, err)
				connections.Store(i, conn)
			}(req.Clone())
		}

		wg.Wait()
		connFirst, _ := connections.Load(0)
		connections.Range(func(key, value any) bool {
			assert.Equal(t, connFirst, value)
			assert.Equal(t, connFirst.(Connection), value.(Connection))
			return true
		})
	}

	t.Run("ParallelUDP", func(t *testing.T) {
		testParallel(t, "UDP")
	})
	t.Run("ParallelTCP", func(t *testing.T) {
		l, err := net.Listen("tcp4", "127.0.0.1:5066")
		require.NoError(t, err)
		defer l.Close()
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					break
				}
				go func() { conn.Read([]byte{}) }()
			}
		}()
		testParallel(t, "TCP")
	})

}

func TestTransportLayerClientConnectionNoReuse(t *testing.T) {
	// NOTE it creates real network connection
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil, WithTransportLayerConnectionReuse(false))
	defer func() {
		require.Empty(t, tp.udp.pool.Size())
	}()
	defer func() {
		require.NoError(t, tp.Close())
	}()

	t.Run("Default", func(t *testing.T) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		require.NotEqual(t, conn, conn2)
	})

	t.Run("WithClientHostPort", func(t *testing.T) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 12345, Params: NewParams()})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 12345, Params: NewParams()})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)
		require.Equal(t, conn, conn2)

		// Now same destination but forcing port
		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 9876, Params: NewParams()})
		req.Laddr = testCreateAddr(t, "127.0.0.1:9876")
		conn3, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		require.NotEqual(t, conn, conn3)
	})
}

func TestTransportLayerDefaultPort(t *testing.T) {
	// NOTE it creates real network connection

	// TODO add other transports
	for _, tran := range []string{"UDP"} {
		t.Run(tran, func(t *testing.T) {
			tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
			req := NewRequest(OPTIONS, Uri{Host: "127.0.0.99"})
			req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

			_, err := tp.ClientRequestConnection(context.TODO(), req)
			require.NoError(t, err)

			tp.Close()
			require.Equal(t, "127.0.0.99:5060", req.Destination())
		})
	}
}

func TestTransportLayerResolving(t *testing.T) {
	// NOTE it creates real network connection

	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	addr := Addr{}
	err := tp.resolveAddr(context.TODO(), "udp", "localhost", "sip", &addr)
	require.NoError(t, err)

	assert.True(t, addr.IP.To4() != nil)
	assert.Equal(t, "127.0.0.1:0", addr.String())
}
