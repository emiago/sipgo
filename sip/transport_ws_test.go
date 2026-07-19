package sip

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readFrameAsync reads one frame from conn without blocking the caller.
func readFrameAsync(t *testing.T, conn net.Conn) <-chan ws.Frame {
	t.Helper()
	ch := make(chan ws.Frame, 1)
	go func() {
		f, err := ws.ReadFrame(conn)
		if err != nil {
			return
		}
		ch <- f
	}()
	return ch
}

func mustReadFrame(t *testing.T, ch <-chan ws.Frame) ws.Frame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame")
		return ws.Frame{}
	}
}

// TestWSConnectionPingIsAnsweredWithPong checks RFC 6455 section 5.5.2: a Pong
// must carry the same payload as the Ping it answers. The text frame sent after
// the ping must still decode, which only holds if the ping payload was read out
// of the stream instead of being left for the next header read.
func TestWSConnectionPingIsAnsweredWithPong(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	c := newWSConnection(serverConn, false, 1)
	defer c.Close()

	readCh := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		readCh <- string(buf[:n])
	}()

	// Client side must mask its frames.
	go func() {
		ping := ws.MaskFrameInPlace(ws.NewPingFrame([]byte("keepalive")))
		if err := ws.WriteFrame(clientConn, ping); err != nil {
			return
		}
		text := ws.MaskFrameInPlace(ws.NewTextFrame([]byte("OPTIONS sip:x SIP/2.0\r\n\r\n")))
		_ = ws.WriteFrame(clientConn, text)
	}()

	pong := mustReadFrame(t, readFrameAsync(t, clientConn))
	assert.Equal(t, ws.OpPong, pong.Header.OpCode)
	assert.Equal(t, []byte("keepalive"), pong.Payload, "pong must echo the ping payload")

	select {
	case got := <-readCh:
		assert.Equal(t, "OPTIONS sip:x SIP/2.0\r\n\r\n", got, "text frame after ping must still decode")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: stream desynced after ping with payload")
	}
}

// TestWSConnectionEmptyPingIsAnsweredWithPong covers the common empty ping,
// which the control handler answers on a header-only fast path.
func TestWSConnectionEmptyPingIsAnsweredWithPong(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	c := newWSConnection(serverConn, false, 1)
	defer c.Close()

	go func() {
		buf := make([]byte, 1024)
		_, _ = c.Read(buf)
	}()

	go func() {
		_ = ws.WriteFrame(clientConn, ws.MaskFrameInPlace(ws.NewPingFrame(nil)))
	}()

	pong := mustReadFrame(t, readFrameAsync(t, clientConn))
	assert.Equal(t, ws.OpPong, pong.Header.OpCode)
	assert.Empty(t, pong.Payload)
}

// TestWSConnectionKeepaliveSendsPing checks that an idle connection emits Ping
// frames on the configured period.
func TestWSConnectionKeepaliveSendsPing(t *testing.T) {
	prev := TransportWSKeepAlivePeriod
	TransportWSKeepAlivePeriod = 10 * time.Millisecond
	t.Cleanup(func() { TransportWSKeepAlivePeriod = prev })

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	c := newWSConnection(serverConn, false, 1)
	defer c.Close()
	go c.keepalive(DefaultLogger())

	f := mustReadFrame(t, readFrameAsync(t, clientConn))
	assert.Equal(t, ws.OpPing, f.Header.OpCode)
	assert.False(t, f.Header.Masked, "server side must not mask")
}

// TestWSConnectionKeepaliveMasksClientSide checks a client side connection masks
// its Ping frames, as required for client to server frames.
func TestWSConnectionKeepaliveMasksClientSide(t *testing.T) {
	prev := TransportWSKeepAlivePeriod
	TransportWSKeepAlivePeriod = 10 * time.Millisecond
	t.Cleanup(func() { TransportWSKeepAlivePeriod = prev })

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	c := newWSConnection(serverConn, true, 1)
	defer c.Close()
	go c.keepalive(DefaultLogger())

	f := mustReadFrame(t, readFrameAsync(t, clientConn))
	assert.Equal(t, ws.OpPing, f.Header.OpCode)
	assert.True(t, f.Header.Masked, "client side must mask")
}

// TestWSConnectionKeepaliveStopsOnClose checks the keepalive goroutine exits when
// the connection is closed, rather than leaking for the process lifetime. The
// period is long enough that the ticker cannot fire, so only Close can end it.
func TestWSConnectionKeepaliveStopsOnClose(t *testing.T) {
	prev := TransportWSKeepAlivePeriod
	TransportWSKeepAlivePeriod = time.Hour
	t.Cleanup(func() { TransportWSKeepAlivePeriod = prev })

	for _, tc := range []struct {
		name  string
		close func(c *WSConnection)
	}{
		{"Close", func(c *WSConnection) { _ = c.Close() }},
		{"TryClose", func(c *WSConnection) { _, _ = c.TryClose() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			defer clientConn.Close()

			c := newWSConnection(serverConn, false, 1)
			done := make(chan struct{})
			go func() {
				c.keepalive(DefaultLogger())
				close(done)
			}()

			tc.close(c)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("keepalive goroutine leaked after close")
			}
		})
	}
}

// TestWSConnectionKeepaliveExitsOnDeadPeer checks that when the peer is gone and
// the ping write fails, the keepalive goroutine closes the connection and exits
// instead of spinning on a dead link.
func TestWSConnectionKeepaliveExitsOnDeadPeer(t *testing.T) {
	prev := TransportWSKeepAlivePeriod
	TransportWSKeepAlivePeriod = 10 * time.Millisecond
	t.Cleanup(func() { TransportWSKeepAlivePeriod = prev })

	serverConn, clientConn := net.Pipe()
	c := newWSConnection(serverConn, false, 1)

	done := make(chan struct{})
	go func() {
		c.keepalive(DefaultLogger())
		close(done)
	}()

	// Peer disappears. The pipe write then fails rather than blocking.
	require.NoError(t, clientConn.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive did not exit after ping write failed")
	}

	// The connection must be closed so the read goroutine wakes up.
	_, err := serverConn.Read(make([]byte, 1))
	assert.Error(t, err)
}

// TestWSConnectionKeepaliveDisabled checks a zero period turns pings off.
func TestWSConnectionKeepaliveDisabled(t *testing.T) {
	prev := TransportWSKeepAlivePeriod
	TransportWSKeepAlivePeriod = 0
	t.Cleanup(func() { TransportWSKeepAlivePeriod = prev })

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	c := newWSConnection(serverConn, false, 1)
	defer c.Close()

	done := make(chan struct{})
	go func() {
		c.keepalive(DefaultLogger())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive should return immediately when disabled")
	}
}

// TestWSConnectionConcurrentWrites exercises pings racing SIP writes. Frames must
// not interleave on the wire, so every frame the peer reads has to decode. Run
// with -race to also catch unsynchronized access to the underlying conn.
func TestWSConnectionConcurrentWrites(t *testing.T) {
	prev := TransportWSKeepAlivePeriod
	TransportWSKeepAlivePeriod = time.Millisecond
	t.Cleanup(func() { TransportWSKeepAlivePeriod = prev })

	const writers, perWriter = 8, 50

	serverConn, clientConn := net.Pipe()

	c := newWSConnection(serverConn, false, 1)
	go c.keepalive(DefaultLogger())

	// Deadlines bound both ends. If writes interleave the peer sees a corrupt
	// frame and stops reading, which would otherwise block the writers on the
	// unbuffered pipe forever and hang the test instead of failing it.
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, serverConn.SetWriteDeadline(time.Now().Add(10*time.Second)))

	type result struct {
		texts int
		bad   string
	}
	resCh := make(chan result, 1)
	go func() {
		res := result{}
		for {
			f, err := ws.ReadFrame(clientConn)
			if err != nil {
				resCh <- res
				return
			}
			if f.Header.OpCode != ws.OpText {
				continue // keepalive pings
			}
			res.texts++
			if string(f.Payload) != "SIP" {
				res.bad = string(f.Payload)
				resCh <- res
				return
			}
			if res.texts == writers*perWriter {
				resCh <- res
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if _, err := c.Write([]byte("SIP")); err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()

	res := <-resCh
	assert.Empty(t, res.bad, "frames interleaved on the wire, peer decoded a corrupt text frame")
	assert.Equal(t, writers*perWriter, res.texts, "peer did not receive every SIP frame intact")

	require.NoError(t, c.Close())
	_ = clientConn.Close()
}
