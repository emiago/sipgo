package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	sipgo "github.com/emiago/sipgo/sip"
	"github.com/livekit/sipgo/fakes"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestTCPBindRefused(t *testing.T) {
	conn, err := bindRange(10000, 10001, func(lport int) (*net.TCPConn, error) {
		return net.DialTCP("tcp", &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: lport,
		}, &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 9999, // dial would fail
		})
	})
	if err == nil {
		conn.Close()
		t.Fatal("expected error")
	}
	if err == ErrCannotBindPort {
		t.Fatal("unexpected error")
	}
}

func TestTCPBindNoPorts(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:10000")
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Close()
	l2, err := net.Listen("tcp", "127.0.0.1:10001")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	conn, err := bindRange(10000, 10001, func(lport int) (*net.TCPConn, error) {
		return net.DialTCP("tcp", &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: lport,
		}, &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 9999, // dial would fail
		})
	})
	if err == nil {
		conn.Close()
		t.Fatal("expected error")
	}
	if err != ErrCannotBindPort {
		t.Fatal("unexpected error")
	}
}

type Test struct {
	name    string
	portMin int
	portMax int
}

type Category struct {
	name  string
	tests []Test
}

func getPortFunc(t *testing.T, min, max int, desired_attempts int, attemptPtr *int) func(port int) (int, error) {
	return func(port int) (int, error) {
		t.Logf("Attempting port %d, min %d, max %d, attempts %d/%d", port, min, max, *attemptPtr, desired_attempts)
		// Calculate normalized bounds for verification
		// Special case: when both min and max are <= 0, bindRange calls create(0) directly
		if min <= 0 && max <= 0 {
			if port == 0 {
				*attemptPtr++
				return port, nil
			}
			t.Fatalf("unexpected port %d when both min and max are <= 0", port)
		}
		normMin := min
		if normMin <= 0 {
			normMin = 1
		}
		normMax := max
		if normMax <= 0 {
			normMax = 0xFFFF
		}
		if normMin > normMax {
			t.Fatalf("Invalid range: min %d > max %d", normMin, normMax)
		}

		// Verify port is within valid TCP port range
		if port <= 0 || port < normMin {
			t.Fatalf("Port %d is below 0x0000/%d", port, min)
		}
		if port > 0xFFFF || port > normMax {
			t.Fatalf("Port %d exceeds 0xFFFF/%d", port, max)
		}

		*attemptPtr++
		if *attemptPtr >= desired_attempts {
			return port, nil
		}
		return 0, syscall.EADDRINUSE
	}
}

func runTest(t *testing.T, errorExpected bool, test Test) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		value int
		err   error
	}

	done := make(chan result, 1)

	go func() {
		expectedMin := test.portMin
		if expectedMin <= 0 {
			expectedMin = 1
		}
		expectedMax := test.portMax
		if expectedMax <= 0 {
			expectedMax = 0xFFFF
		}

		attempts := int(0)
		desired_attempts := (expectedMax - expectedMin + 1)
		create := getPortFunc(t, test.portMin, test.portMax, desired_attempts, &attempts)
		res, err := bindRange(test.portMin, test.portMax, create)
		done <- result{value: res, err: err}
	}()

	var res result
	select {
	case <-ctx.Done():
		t.Fatal("Test timed out after 5 seconds")
	case res = <-done:
		// Test completed, continue with assertions
	}

	expectedMin := test.portMin
	if expectedMin <= 0 {
		expectedMin = 1
	}
	expectedMax := test.portMax
	if expectedMax <= 0 {
		expectedMax = 0xFFFF
	}
	if res.err != nil {
		// Allow error if range is invalid (min > max after normalization)
		// Calculate normalized values to check
		if expectedMin > expectedMax {
			// This is expected for invalid ranges
			return
		}
		// Also allow errors for "invalid" category tests
		if errorExpected {
			return
		}
		t.Errorf("Unexpected error: %v", res.err)
		return
	}

	// Verify result is within bounds
	if res.value < 0 {
		t.Errorf("Result port %d is below 0x0000", res.value)
	}
	if res.value > 0xFFFF {
		t.Errorf("Result port %d exceeds 0xFFFF", res.value)
	}
	// Special case: when both min and max are <= 0, bindRange returns port 0
	if test.portMin <= 0 && test.portMax <= 0 {
		if res.value != 0 {
			t.Errorf("Result port %d is not 0 when both min and max are <= 0", res.value)
		}
		return
	}
	if res.value < expectedMin {
		t.Errorf("Result port %d is below portMin (%d)", res.value, expectedMin)
	}
	if res.value > expectedMax {
		t.Errorf("Result port %d exceeds portMax (%d)", res.value, expectedMax)
	}
}

func runTests(t *testing.T, name string, errorExpected bool, tests []Test) {
	for _, tt := range tests {
		t.Run(name+"/"+tt.name, func(t *testing.T) {
			runTest(t, errorExpected, tt)
		})
	}
}

func TestBindRange_Normal(t *testing.T) {
	runTests(t, "normal", false, []Test{
		{"normal range 1", 1, 10},
		{"normal range 2", 5000, 5010},
		{"normal range 3", 65000, 65010},
		{"normal range 4", 65530, 65535},
	})
}

func TestBindRange_Bounds(t *testing.T) {
	runTests(t, "bounds", false, []Test{
		{"normal range 1", 0, 10},
		{"normal range 4", 65530, 0},
	})
}

func TestBindRange_Edges(t *testing.T) {
	runTests(t, "edges", false, []Test{
		{"min edge", 1, 1},
		{"max edge", 65535, 65535},
		{"full range", 1, 65535},
		{"full range implicit", 0, 0},
	})
}

func TestBindRange_Negative(t *testing.T) {
	runTests(t, "negative", false, []Test{
		{"negative min", -10, 10},
		{"negative max", 10, -10},
		{"both negative", -10, -5},
	})
}

func TestBindRange_Invalid(t *testing.T) {
	runTests(t, "invalid", true, []Test{
		{"min > max", 20, 5},
		{"min > max min negative", -20, 5},
		{"min > max min implicit", 0, 5},
		{"min > max max negative", 20, -5},
		{"min > max max implicit", 9876543, 0},
		{"min > max", 2000, 1000},
		{"min > max edge", 65535, 1},
		{"min > max large", 50000, 1000},
	})
}

func TestBindRange_Large(t *testing.T) {
	runTests(t, "large", false, []Test{
		{"large range 1", 1000, 65535},
		{"large range 2", 1, 50000},
		{"large range 3", 10000, 65535},
	})
}

// A connection that receives nothing before the read deadline must fail its
// read, so readConnection returns and the deferred pool cleanup runs. This
// catches a peer that went away without a FIN or RST.
func TestTCPConnectionReadTimeout(t *testing.T) {
	prev := TCPReadTimeout
	TCPReadTimeout = 25 * time.Millisecond
	defer func() { TCPReadTimeout = prev }()

	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	c := &TCPConnection{Conn: local}
	if _, err := c.Read(make([]byte, 128)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
}

// With the deadline disabled a read must still block rather than fail.
func TestTCPConnectionReadTimeoutDisabled(t *testing.T) {
	prev := TCPReadTimeout
	TCPReadTimeout = 0
	defer func() { TCPReadTimeout = prev }()

	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	c := &TCPConnection{Conn: local}
	read := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 128))
		read <- err
	}()

	select {
	case err := <-read:
		t.Fatalf("read returned without any data: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := remote.Write([]byte("\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A message that stops mid write and is never completed must not swallow the
// next message on that connection. After a whole TCPReadTimeout with nothing
// arriving, the fragment is dropped and the stream realigns, without closing a
// connection the peer may never rebuild.
func TestReadConnectionDropsAbandonedMessage(t *testing.T) {
	prev := TCPReadTimeout
	TCPReadTimeout = 50 * time.Millisecond

	tr := NewTCPTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), sipgo.NewParser(), nil)
	local, remote := net.Pipe()

	msgs := make(chan sipgo.Message, 4)
	tr.initConnection(local, "10.2.2.2:5060", func(msg sipgo.Message) { msgs <- msg })

	defer func() {
		// Close the peer end so the read loop exits on its own, and wait for it
		// to leave the pool before restoring the timeout it reads on every read.
		remote.Close()
		for i := 0; i < 200 && tr.pool.Size() > 0; i++ {
			time.Sleep(5 * time.Millisecond)
		}
		tr.Close()
		TCPReadTimeout = prev
	}()

	// A message that stops inside a header value, before its Content-Length.
	truncated := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 10.0.0.1:5060;branch=z9hG4bK.trunc\r\n" +
		"Call-ID: TRUNCATED\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Min-SE:"
	if _, err := remote.Write([]byte(truncated)); err != nil {
		t.Fatal(err)
	}

	// Nothing follows it for longer than the read deadline.
	time.Sleep(4 * TCPReadTimeout)

	if _, err := remote.Write([]byte(invite("SECOND", 40))); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-msgs:
		if got := msg.CallID().Value(); got != "SECOND" {
			t.Fatalf("expected Call-ID SECOND, got %q", got)
		}
		via := msg.Via()
		if via == nil {
			t.Fatal("message has no Via")
		}
		// Without the reset the fragment merges in and this carries the
		// truncated message's branch instead.
		if strings.Contains(via.Value(), "z9hG4bK.trunc") {
			t.Fatalf("message merged with the abandoned fragment: %q", via.Value())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message delivered")
	}
}

// One abandoned message is dropped and the connection kept, but a peer that
// abandons a second one with nothing framed in between is not recovering, so
// the connection goes.
func TestReadConnectionClosesOnRepeatedAbandonedMessage(t *testing.T) {
	prev := TCPReadTimeout
	TCPReadTimeout = 50 * time.Millisecond

	tr := NewTCPTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), sipgo.NewParser(), nil)
	local, remote := net.Pipe()

	defer func() {
		remote.Close()
		tr.Close()
		TCPReadTimeout = prev
	}()

	tr.initConnection(local, "10.2.2.2:5060", func(sipgo.Message) {})

	head := "INVITE sip:bob@example.com SIP/2.0\r\nCall-ID: %s\r\nCSeq: 1 INVITE\r\nMin-SE:"
	for i, id := range []string{"FIRST", "SECOND"} {
		if _, err := remote.Write([]byte(fmt.Sprintf(head, id))); err != nil {
			t.Fatalf("fragment %d: %v", i, err)
		}
		time.Sleep(4 * TCPReadTimeout)
	}

	// The read loop leaves the pool on its way out.
	require.Eventually(t, func() bool { return tr.pool.Size() == 0 }, 2*time.Second, 5*time.Millisecond,
		"expected the connection to be closed")
}

// A TLS handshake that times out is saved by crypto/tls and returned by every
// later Read without waiting, so the read loop must close the connection
// rather than keep it as idle.
func TestReadConnectionClosesOnFailedTLSHandshake(t *testing.T) {
	prev := TCPReadTimeout
	TCPReadTimeout = 50 * time.Millisecond

	tr := NewTCPTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), sipgo.NewParser(), nil)
	local, remote := net.Pipe()

	defer func() {
		remote.Close()
		tr.Close()
		TCPReadTimeout = prev
	}()

	closed := connectionClosed.WithLabelValues(tr.Network(), "tls_handshake_did_not_complete")
	before := testutil.ToFloat64(closed)

	// The peer never sends a ClientHello.
	tr.initConnection(tls.Server(local, &tls.Config{}), "10.2.2.2:5061", func(sipgo.Message) {})

	require.Eventually(t, func() bool { return tr.pool.Size() == 0 }, 2*time.Second, 5*time.Millisecond,
		"expected the connection to be closed")
	require.Equal(t, before+1, testutil.ToFloat64(closed))
}

// A write that times out must take the connection out of the pool before
// WriteMsg returns, so the next request to that address dials a new one rather
// than failing the same way.
func TestWriteMsgFailureDropsConnection(t *testing.T) {
	prev := TCPWriteTimeout
	TCPWriteTimeout = 25 * time.Millisecond

	tr := NewTCPTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), sipgo.NewParser(), nil)
	local, remote := net.Pipe()

	defer func() {
		remote.Close()
		tr.Close()
		TCPWriteTimeout = prev
	}()

	closed := connectionClosed.WithLabelValues(tr.Network(), "write_error")
	before := testutil.ToFloat64(closed)

	// The peer never reads, so the write cannot complete.
	c := tr.initConnection(local, "10.2.2.2:5060", func(sipgo.Message) {})

	req := sipgo.NewRequest(sipgo.OPTIONS, sipgo.Uri{Scheme: "sip", Host: "10.2.2.2", Port: 5060})
	err := c.WriteMsg(req)
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.Zero(t, tr.pool.Size())

	require.Eventually(t, func() bool { return testutil.ToFloat64(closed) == before+1 }, 2*time.Second, 5*time.Millisecond)
}

// Cleanup of a dropped connection must not evict a newer connection that has
// taken its address in the meantime.
func TestCloseAndDeleteKeepsReplacement(t *testing.T) {
	pool := NewConnectionPool()
	newConn := func() *TCPConnection {
		return &TCPConnection{Conn: &fakes.TCPConn{
			LAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
			RAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
		}, log: slog.Default()}
	}
	addr := "127.0.0.2:5060"

	old, replacement := newConn(), newConn()
	pool.Add(addr, old)
	pool.Add(addr, replacement)

	pool.CloseAndDelete(slog.New(slog.NewTextHandler(io.Discard, nil)), old, addr)
	require.Same(t, replacement, pool.Get(addr))
}

// warnCounter counts log records at Warn or above.
type warnCounter struct {
	slog.Handler
	n atomic.Int32
}

func (h *warnCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.n.Add(1)
	}
	return nil
}

func (h *warnCounter) WithAttrs([]slog.Attr) slog.Handler { return h }

// A transaction that still holds a connection when the peer closes it releases
// its reference afterwards. That is expected, not a refcount bug.
func TestReleaseAfterPeerCloseIsQuiet(t *testing.T) {
	warns := &warnCounter{Handler: slog.NewTextHandler(io.Discard, nil)}
	tr := NewTCPTransport(slog.New(warns), sipgo.NewParser(), nil)
	defer tr.Close()
	local, remote := net.Pipe()

	addr := "10.2.2.2:5060"
	tr.initConnection(local, addr, func(sipgo.Message) {})
	c := tr.pool.Get(addr)
	require.NotNil(t, c)

	remote.Close()
	require.Eventually(t, func() bool { return tr.pool.Size() == 0 }, 2*time.Second, 5*time.Millisecond)

	ref, err := c.TryClose()
	require.NoError(t, err)
	require.Zero(t, ref)
	require.Zero(t, warns.n.Load())
}
