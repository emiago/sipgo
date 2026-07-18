package sipgo

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// listenReady returns a context that reports the address a listener binds to,
// using the library's own ListenReady hook. Binding to port 0 and learning the
// result this way avoids racing the server for a fixed port.
func listenReady(t *testing.T, parent context.Context) (context.Context, <-chan string) {
	t.Helper()

	ch := make(chan string, 1)
	var once sync.Once
	fn := ListenReadyFuncCtxValue(func(network, addr string) {
		once.Do(func() { ch <- addr })
	})
	return context.WithValue(parent, ListenReadyCtxKey, fn), ch
}

// waitPortFree reports whether addr can be bound again, retrying briefly since
// the close is asynchronous.
func waitPortFree(addr string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		c, err := net.ListenPacket("udp", addr)
		if err == nil {
			c.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	ua, err := NewUA()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ua.Close() })

	srv, err := NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestListenAndServeClosesListenerOnCancel checks that cancelling the context
// actually closes the listener.
//
// Previously the closer goroutine started before the listener existed and read
// a variable the caller assigned afterwards. That was a data race, and when the
// goroutine observed the pre-assignment nil it returned early and the socket
// was never closed. Run with -race.
func TestListenAndServeClosesListenerOnCancel(t *testing.T) {
	srv := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	ctx, ready := listenReady(t, ctx)

	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServe(ctx, "udp", "127.0.0.1:0") }()

	var addr string
	select {
	case addr = <-ready:
	case err := <-served:
		cancel()
		t.Fatalf("ListenAndServe returned before listening: %v", err)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("listener never became ready")
	}

	cancel()

	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not return after cancel")
	}

	if !waitPortFree(addr, 2*time.Second) {
		t.Fatalf("listener at %s was not closed on cancel", addr)
	}
}

// TestListenAndServeCancelledBeforeBind covers the case the old code leaked
// most reliably: a context already done by the time the listener is created.
// The closer goroutine saw a nil closer, returned, and nothing ever closed the
// socket.
func TestListenAndServeCancelledBeforeBind(t *testing.T) {
	srv := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	ctx, ready := listenReady(t, ctx)
	cancel() // already done before ListenAndServe runs

	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServe(ctx, "udp", "127.0.0.1:0") }()

	var addr string
	select {
	case addr = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("listener never reported ready")
	}

	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}

	if !waitPortFree(addr, 2*time.Second) {
		t.Fatalf("listener at %s leaked when context was cancelled before bind", addr)
	}
}
