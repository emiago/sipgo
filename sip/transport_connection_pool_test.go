package sip

import (
	"bytes"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
)

func TestConnectionPool(t *testing.T) {
	pool := newConnectionPool()

	fakeConn := &fakes.TCPConn{
		LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
		Reader: nil,
		Writer: nil,
	}
	conn := &TCPConnection{Conn: fakeConn}

	pool.Add(fakeConn.RAddr.String(), conn)

	c := pool.Get(fakeConn.RAddr.String())
	if c != conn {
		t.Fatal("Not found connection")
	}
}

func BenchmarkConnectionPool(b *testing.B) {
	pool := newConnectionPool()

	for i := 0; i < b.N; i++ {
		conn := &TCPConnection{Conn: &fakes.TCPConn{
			LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
			RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
			Reader: nil,
			Writer: nil,
		}}
		a := &net.TCPAddr{
			IP:   net.IPv4('1', '2', '3', byte(i)),
			Port: 1000,
		}
		pool.Add(a.String(), conn)
		c := pool.Get(a.String())
		if c != conn {
			b.Fatal("mismatched function")
		}
	}
}

// closeCountingConn counts Close calls so a test can tell a connection that was
// closed once from one closed under an owner that still holds a reference.
type closeCountingConn struct {
	*fakes.TCPConn
	closes atomic.Int32
}

func (c *closeCountingConn) Close() error {
	c.closes.Add(1)
	return nil
}

func newCountingConn() *closeCountingConn {
	return &closeCountingConn{TCPConn: &fakes.TCPConn{
		LAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
	}}
}

// CloseAndDelete must not close a connection other owners still hold: the pool
// is built on reference counting and the last reference does the closing.
func TestConnectionPoolCloseAndDeleteKeepsLiveReference(t *testing.T) {
	pool := newConnectionPool()
	fake := newCountingConn()
	// 1 reference for the reader, 1 for an owner in the middle of a request
	conn := &TCPConnection{Conn: fake, refcount: 2}
	pool.Add(fake.RAddr.String(), conn)

	if err := pool.CloseAndDelete(conn, fake.RAddr.String()); err != nil {
		t.Fatalf("CloseAndDelete: %v", err)
	}
	if n := fake.closes.Load(); n != 0 {
		t.Fatalf("connection closed %d times with a reference left", n)
	}
	if ref := conn.Ref(0); ref != 1 {
		t.Fatalf("references left = %d, want 1", ref)
	}

	if _, err := conn.TryClose(); err != nil {
		t.Fatalf("TryClose: %v", err)
	}
	if n := fake.closes.Load(); n != 1 {
		t.Fatalf("connection closed %d times after the last reference, want 1", n)
	}
}

// A connection is stored under more than one key, so closing it leaves the
// other keys pointing at a socket that is gone. Handing it out again also pulls
// its reference count back above zero, and the next TryClose closes it twice.
func TestConnectionPoolDoesNotReturnClosedConnection(t *testing.T) {
	pool := newConnectionPool()
	fake := newCountingConn()
	conn := &TCPConnection{Conn: fake, refcount: 1}
	pool.Add(fake.RAddr.String(), conn)
	pool.Add(fake.LAddr.String(), conn)

	if err := pool.CloseAndDelete(conn, fake.RAddr.String()); err != nil {
		t.Fatalf("CloseAndDelete: %v", err)
	}
	if n := fake.closes.Load(); n != 1 {
		t.Fatalf("last reference closed the connection %d times, want 1", n)
	}
	if c := pool.Get(fake.LAddr.String()); c != nil {
		t.Fatal("pool returned a closed connection")
	}
	if n := fake.closes.Load(); n != 1 {
		t.Fatalf("connection closed %d times in total, want 1", n)
	}
}

// The reader exiting takes the connection out of the pool, so the idle
// reference kept for reuse has to go with it. Otherwise nothing ever releases
// it and the socket stays open for the life of the process.
func TestTCPReadConnectionReleasesIdleReference(t *testing.T) {
	layer := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	t.Cleanup(func() { _ = layer.Close() })

	fake := newCountingConn()
	// an empty reader ends the read loop at once, as a peer closing would
	fake.Reader = bytes.NewReader(nil)
	conn := layer.tcp.initConnection(fake, fake.RAddr.String(), func(m Message) {})

	deadline := time.Now().Add(time.Second)
	for conn.Ref(0) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ref := conn.Ref(0); ref != 0 {
		t.Fatalf("references left after the reader stopped = %d, want 0", ref)
	}
	if n := fake.closes.Load(); n != 1 {
		t.Fatalf("connection closed %d times, want 1", n)
	}
	if c := layer.tcp.GetConnection(fake.RAddr.String()); c != nil {
		t.Fatal("closed connection left in the pool")
	}
}

// A UDP listener sits in the pool under every peer it accepted, and those keys
// are dropped one at a time. Once its last reference is gone its reader has
// stopped, so the pool must not hand it out: the caller would be sent to a
// socket nothing reads instead of dialing a new connection.
//
// TryClose returns early for a listener — closing the socket is the caller's
// job — and used to return before marking the connection closed, so the flag
// that keeps closed connections out of the pool never got set for the one
// connection type that has the most keys.
func TestConnectionPoolDoesNotReturnClosedListener(t *testing.T) {
	pool := newConnectionPool()
	fake := &fakes.UDPConn{
		LAddr: net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr: net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
	}
	conn := &UDPConnection{PacketConn: fake, PacketAddr: fake.LAddr.String(), Listener: true}

	// the address it listens on, plus a peer it accepted
	pool.Add(fake.LAddr.String(), conn)
	pool.Add(fake.RAddr.String(), conn)

	if err := pool.CloseAndDelete(conn, fake.LAddr.String()); err != nil {
		t.Fatalf("CloseAndDelete: %v", err)
	}
	if !conn.Closed() {
		t.Fatal("listener not marked closed after its last reference was released")
	}
	if c := pool.Get(fake.RAddr.String()); c != nil {
		t.Fatal("pool returned a closed listener")
	}
}

// A listener other owners still hold is not marked closed: they may still send
// on it, and the socket is closed by whoever opened it, not by the pool.
func TestConnectionPoolKeepsListenerWithLiveReference(t *testing.T) {
	pool := newConnectionPool()
	fake := &fakes.UDPConn{
		LAddr: net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr: net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
	}
	// 1 reference for the reader, 1 for an owner in the middle of a request
	conn := &UDPConnection{
		PacketConn: fake, PacketAddr: fake.LAddr.String(),
		Listener: true, refcount: 2,
	}
	pool.Add(fake.LAddr.String(), conn)
	pool.Add(fake.RAddr.String(), conn)

	if err := pool.CloseAndDelete(conn, fake.LAddr.String()); err != nil {
		t.Fatalf("CloseAndDelete: %v", err)
	}
	if conn.Closed() {
		t.Fatal("listener marked closed with a reference left")
	}
	if c := pool.Get(fake.RAddr.String()); c == nil {
		t.Fatal("pool dropped a listener its owner still holds")
	}
}
