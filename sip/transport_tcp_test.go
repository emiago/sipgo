package sip

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
)

func newFakeTCPConn() *fakes.TCPConn {
	return &fakes.TCPConn{
		LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
		Reader: bytes.NewReader(nil),
		Writer: io.Discard,
	}
}

// TestTCPConnectionWriteTimeout checks that a connection only arms a write
// deadline when the transport was configured with a positive WriteTimeout.
// The zero value must stay a strict no-op so existing users see no change.
func TestTCPConnectionWriteTimeout(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		fc := newFakeTCPConn()
		c := &TCPConnection{Conn: fc}

		if _, err := c.Write([]byte("OPTIONS sip:example.com SIP/2.0\r\n")); err != nil {
			t.Fatalf("write err=%v", err)
		}

		if got := fc.WriteDeadlines(); len(got) != 0 {
			t.Fatalf("zero WriteTimeout must not arm a deadline, got %d calls", len(got))
		}
	})

	t.Run("arms deadline when configured", func(t *testing.T) {
		fc := newFakeTCPConn()
		c := &TCPConnection{Conn: fc, writeTimeout: time.Minute}

		before := time.Now()
		if _, err := c.Write([]byte("REGISTER sip:example.com SIP/2.0\r\n")); err != nil {
			t.Fatalf("write err=%v", err)
		}
		after := time.Now()

		got := fc.WriteDeadlines()
		if len(got) != 1 {
			t.Fatalf("expected exactly one deadline armed, got %d", len(got))
		}
		// Deadline must land in [before+timeout, after+timeout].
		if got[0].Before(before.Add(time.Minute)) || got[0].After(after.Add(time.Minute)) {
			t.Fatalf("deadline %v not within one minute of the write", got[0])
		}
	})
}

// TestTransportTCPWriteTimeoutPropagates checks that the transport hands its
// WriteTimeout to the connections it accepts, which is what makes the field
// reachable through TransportsConfig.
func TestTransportTCPWriteTimeoutPropagates(t *testing.T) {
	tp := &TransportTCP{WriteTimeout: 3 * time.Second}
	tp.init(NewParser())
	defer tp.Close()

	fc := newFakeTCPConn()
	conn := tp.initConnection(fc, fc.RemoteAddr().String(), func(msg Message) {})

	got := conn.(*TCPConnection).writeTimeout
	if got != 3*time.Second {
		t.Fatalf("expected connection to inherit WriteTimeout, got %v", got)
	}
}

// TestTransportTCPReadTimeout checks that the read loop only arms a read
// deadline when the transport was configured with a positive ReadTimeout. The
// zero value must stay a strict no-op so existing users see no change.
func TestTransportTCPReadTimeout(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		tp := &TransportTCP{}
		tp.init(NewParser())
		defer tp.Close()

		fc := newFakeTCPConn()
		tp.readConnection(&TCPConnection{Conn: fc}, fc.LocalAddr().String(), fc.RemoteAddr().String(), func(msg Message) {})

		if got := fc.ReadDeadlines(); len(got) != 0 {
			t.Fatalf("expected no read deadline armed, got %v", got)
		}
	})

	t.Run("armed when set", func(t *testing.T) {
		tp := &TransportTCP{ReadTimeout: 30 * time.Second}
		tp.init(NewParser())
		defer tp.Close()

		fc := newFakeTCPConn()
		tp.readConnection(&TCPConnection{Conn: fc}, fc.LocalAddr().String(), fc.RemoteAddr().String(), func(msg Message) {})

		if got := fc.ReadDeadlines(); len(got) == 0 {
			t.Fatal("expected a read deadline to be armed")
		}
	})
}

// TestTransportTCPReadTimeoutIdlePeer drives a real net.Pipe, which honors
// deadlines, to show that a peer which connects and then sends nothing is
// dropped rather than holding the read goroutine forever.
func TestTransportTCPReadTimeoutIdlePeer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	tp := &TransportTCP{ReadTimeout: 50 * time.Millisecond}
	tp.init(NewParser())
	defer tp.Close()

	done := make(chan struct{})
	go func() {
		// client never writes, so the read blocks until the deadline fires
		tp.readConnection(&TCPConnection{Conn: server}, "127.0.0.1:5060", "127.0.0.2:5060", func(msg Message) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("read loop did not return: an idle peer held the goroutine open")
	}
}

// TestTCPConnectionWriteTimeoutStalledPeer drives a real net.Pipe, which
// honors deadlines, to show that a write to a peer that never reads fails with
// a timeout instead of blocking the writer forever.
func TestTCPConnectionWriteTimeoutStalledPeer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close() // never read from

	c := &TCPConnection{Conn: client, writeTimeout: 50 * time.Millisecond}

	done := make(chan error, 1)
	go func() {
		_, err := c.Write([]byte("INVITE sip:example.com SIP/2.0\r\n"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected stalled write to fail once the deadline passed")
		}
		var ne net.Error
		if !errors.Is(err, os.ErrDeadlineExceeded) && !(errors.As(err, &ne) && ne.Timeout()) {
			t.Fatalf("expected a timeout error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write was not bounded by the deadline")
	}
}
