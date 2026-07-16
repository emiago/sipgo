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
