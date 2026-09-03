package sip

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRawOptions(callID string) []byte {
	return []byte("OPTIONS sip:example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK-test\r\n" +
		"From: <sip:alice@example.com>;tag=from1\r\n" +
		"To: <sip:bob@example.com>\r\n" +
		"Call-ID: " + callID + "\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n")
}

func TestTransportLayerClosing(t *testing.T) {
	// NOTE it creates real network connection

	// TODO add other transports
	for _, tran := range []string{"UDP"} {
		t.Run(tran, func(t *testing.T) {
			tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
			req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
			req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})

			conn, err := tp.ClientRequestConnection(context.TODO(), req)
			require.NoError(t, err)

			tp.Close()
			c := conn.(*UDPConnection)
			require.Error(t, c.Close(), "It is not closed already")
		})
	}
}

func TestTransportLayerReadFilterUDP(t *testing.T) {
	filterCalls := make(chan TransportReadProps, 2)
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil, WithTransportLayerReadFilter(
		func(info TransportReadProps, data []byte) ([]byte, error) {
			filterCalls <- info
			if bytes.Contains(data, []byte("drop-call")) {
				return nil, nil
			}

			return bytes.ReplaceAll(data, []byte("bad-call"), []byte("good-call")), nil
		},
	))
	defer tp.Close()

	msgs := make(chan Message, 1)
	tp.OnMessage(func(msg Message) {
		msgs <- msg
	})

	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer serverConn.Close()

	go func() {
		_ = tp.ServeUDP(serverConn)
	}()

	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer clientConn.Close()

	serverAddr, err := net.ResolveUDPAddr("udp", serverConn.LocalAddr().String())
	require.NoError(t, err)

	_, err = clientConn.WriteTo(testRawOptions("drop-call"), serverAddr)
	require.NoError(t, err)
	_, err = clientConn.WriteTo(testRawOptions("bad-call"), serverAddr)
	require.NoError(t, err)

	var msg Message
	select {
	case msg = <-msgs:
	case <-time.After(2 * time.Second):
		t.Fatal("expected filtered UDP message")
	}

	require.Equal(t, "good-call", msg.CallID().Value())
	require.Equal(t, "UDP", msg.Transport())
	require.Equal(t, clientConn.LocalAddr().String(), msg.Source())

	var info TransportReadProps
	select {
	case info = <-filterCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("expected UDP filter call")
	}
	require.Equal(t, "UDP", info.Transport)
	require.Equal(t, serverConn.LocalAddr().String(), info.LocalAddr.String())
	require.Equal(t, clientConn.LocalAddr().String(), info.RemoteAddr.String())
}

func TestTransportLayerReadFilterTCPErrorStopsRead(t *testing.T) {
	filterErr := errors.New("stop read")
	filterCalls := make(chan TransportReadProps, 1)
	tcp := &TransportTCP{
		readFilter: func(info TransportReadProps, data []byte) ([]byte, error) {
			filterCalls <- info
			return nil, filterErr
		},
	}
	tcp.init(NewParser())

	closed := make(chan struct{})
	tcp.onConnClose = func(conn Connection) {
		close(closed)
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	conn := &TCPConnection{
		Conn:     serverConn,
		refcount: 1,
	}

	go tcp.readConnection(conn, serverConn.LocalAddr().String(), serverConn.RemoteAddr().String(), func(msg Message) {
		t.Fatal("handler should not be called after read filter error")
	})

	_, err := clientConn.Write(testRawOptions("error-call"))
	require.NoError(t, err)

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("expected TCP read connection to stop")
	}

	select {
	case info := <-filterCalls:
		require.Equal(t, "TCP", info.Transport)
		require.NotNil(t, info.LocalAddr)
		require.NotNil(t, info.RemoteAddr)
	case <-time.After(2 * time.Second):
		t.Fatal("expected TCP filter call")
	}
}

func TestTransportLayerWriteFilterUDP(t *testing.T) {
	type call struct {
		info TransportWriteProps
		data []byte
	}
	writes := make(chan call, 4)
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil,
		WithTransportLayerWriteFilter(func(info TransportWriteProps, data []byte) {
			writes <- call{info: info, data: append([]byte(nil), data...)}
		}),
	)
	defer tp.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer peer.Close()

	req := NewRequest(OPTIONS, Uri{User: "x", Host: "127.0.0.1", Port: peer.LocalAddr().(*net.UDPAddr).Port})
	req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})
	req.AppendHeader(NewHeader("Call-ID", "write-filter-1"))
	req.SetDestination(peer.LocalAddr().String())
	require.NoError(t, tp.WriteMsg(req))

	buf := make([]byte, 65535)
	n, _, err := peer.ReadFrom(buf)
	require.NoError(t, err)

	select {
	case c := <-writes:
		require.Equal(t, "udp", c.info.Transport)
		require.Equal(t, peer.LocalAddr().String(), c.info.RemoteAddr.String())
		require.NotNil(t, c.info.LocalAddr)
		require.Equal(t, buf[:n], c.data, "filter must see the exact bytes that went on the wire")
	case <-time.After(time.Second):
		t.Fatal("write filter not called")
	}
}

func TestTransportLayerWriteFilterTCPSuccess(t *testing.T) {
	type call struct {
		info TransportWriteProps
		data []byte
	}
	writes := make(chan call, 4)
	tcp := &TransportTCP{writeFilter: func(info TransportWriteProps, data []byte) {
		writes <- call{info: info, data: append([]byte(nil), data...)}
	}}
	tcp.init(NewParser())

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	conn := &TCPConnection{Conn: serverConn, refcount: 1, writeFilter: tcp.writeFilter}

	req := NewRequest(OPTIONS, Uri{User: "x", Host: "127.0.0.1", Port: 5060})
	req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})
	req.AppendHeader(NewHeader("Call-ID", "write-filter-tcp-1"))

	// net.Pipe is synchronous and unbuffered: the reader must be running
	// before WriteMsg, or the write blocks forever.
	read := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)
		n, err := clientConn.Read(buf)
		if err != nil {
			close(read)
			return
		}
		read <- buf[:n]
	}()

	require.NoError(t, conn.WriteMsg(req))

	var onWire []byte
	select {
	case b, ok := <-read:
		require.True(t, ok, "peer read failed")
		onWire = b
	case <-time.After(2 * time.Second):
		t.Fatal("peer never received the bytes")
	}

	select {
	case c := <-writes:
		require.Equal(t, "tcp", c.info.Transport)
		require.Equal(t, onWire, c.data, "filter must see the exact bytes that went on the wire")
	case <-time.After(time.Second):
		t.Fatal("write filter not called on a successful TCP write")
	}
	select {
	case <-writes:
		t.Fatal("write filter fired more than once for one message")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTransportLayerWriteFilterTCPNotCalledOnFailedWrite(t *testing.T) {
	called := make(chan struct{}, 1)
	tcp := &TransportTCP{writeFilter: func(TransportWriteProps, []byte) { called <- struct{}{} }}
	tcp.init(NewParser())

	serverConn, clientConn := net.Pipe()
	_ = clientConn.Close() // peer gone: Write must fail
	conn := &TCPConnection{Conn: serverConn, refcount: 1, writeFilter: tcp.writeFilter}
	req := NewRequest(OPTIONS, Uri{User: "x", Host: "127.0.0.1", Port: 5060})
	require.Error(t, conn.WriteMsg(req))
	select {
	case <-called:
		t.Fatal("write filter must not fire when the write failed")
	case <-time.After(100 * time.Millisecond):
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
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		require.Equal(t, conn, conn2)
	})

	t.Run("WithClientHostPort", func(t *testing.T) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "localhost", Port: 12345})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "localhost", Port: 12345})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)
		require.Equal(t, conn, conn2)

		// Now same destination but forcing port
		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 9876})
		req.Laddr = testCreateAddr(t, "127.0.0.1:9876")
		conn3, err := tp.ClientRequestConnection(context.TODO(), req)

		require.NoError(t, err)
		require.NotEqual(t, conn, conn3)
	})

	testParallel := func(t *testing.T, transport string) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})
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
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		require.NotEqual(t, conn, conn2)
	})

	t.Run("WithClientHostPort", func(t *testing.T) {
		req := NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 12345})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)

		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 12345})
		req.Laddr = testCreateAddr(t, "127.0.0.1:12345")

		conn2, err := tp.ClientRequestConnection(context.TODO(), req)
		require.NoError(t, err)
		require.Equal(t, conn, conn2)

		// Now same destination but forcing port
		req = NewRequest(OPTIONS, Uri{Host: "localhost", Port: 5066})
		req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 9876})
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
			req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0})

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
