package sip

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCreateAddr(t *testing.T, addr string) Addr {
	a := Addr{}
	require.NoError(t, a.parseAddr(addr))
	return a
}

func TestIntegrationTransactionLayerServerTx(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	txl := NewTransactionLayer(tp)

	req := testCreateRequest(t, "OPTIONS", "sip:192.168.0.1", "UDP", "127.0.0.1:15069")
	key, _ := ServerTxKeyMake(req)

	var count int32 = 0
	txl.OnRequest(func(req *Request, tx *ServerTx) {
		atomic.AddInt32(&count, 1)
		t.Log("Request")
	})

	// Connection will be created
	err := txl.handleRequest(req)
	require.NoError(t, err)

	// Now create connection and test multiple concurent received request
	tp.udp.CreateConnection(context.TODO(),
		testCreateAddr(t, "127.0.0.1:15069"),
		testCreateAddr(t, "192.168.0.1:1234"),
		tp.handleMessage,
	)

	wg := sync.WaitGroup{}
	wg.Add(3)
	for range []int{0, 1, 2} {
		go func() {
			defer wg.Done()
			err := txl.handleRequest(req)
			if err != nil {
				t.Log("Request failed with err", err)
			}
		}()
	}

	wg.Wait()
	require.EqualValues(t, 1, atomic.LoadInt32(&count))
	require.EqualValues(t, 1, len(txl.serverTransactions.items))

	// After termination of transaction, it  must be removed from list
	tx := txl.serverTransactions.items[key]
	require.NotNil(t, tx)
	tx.Terminate()
	require.EqualValues(t, 0, len(txl.serverTransactions.items))
}

func TestTransactionLayerMalformedRequestStateless400(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	// Listen on a UDP port to receive the stateless 400 response.
	receiverAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	receiverConn, err := net.ListenUDP("udp", receiverAddr)
	require.NoError(t, err)
	defer receiverConn.Close()
	receiverActualAddr := receiverConn.LocalAddr().String()

	// Set up the transaction layer with a real transport.
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	txl := NewTransactionLayer(tp)

	var handlerCalled int32
	txl.OnRequest(func(req *Request, tx *ServerTx) {
		atomic.AddInt32(&handlerCalled, 1)
	})

	// Create a UDP connection in the transport pool so WriteMsg can find it.
	localAddr := "127.0.0.1:15071"
	_, err = tp.udp.CreateConnection(
		context.TODO(),
		testCreateAddr(t, localAddr),
		testCreateAddr(t, receiverActualAddr),
		tp.handleMessage,
	)
	require.NoError(t, err)

	// Build a malformed request: valid Via, From, To, Call-ID, but NO CSeq.
	raw := strings.Join([]string{
		"REGISTER sip:192.168.100.30:5060 SIP/2.0",
		"Via: SIP/2.0/UDP " + receiverActualAddr + ";branch=z9hG4bK-test123",
		"From: <sip:alice@example.com>;tag=from1",
		"To: <sip:alice@example.com>",
		"Call-ID: malformed-test-call-id",
		"Content-Length: 0",
		"",
		"",
	}, "\r\n")

	msg, err := ParseMessage([]byte(raw))
	require.NoError(t, err)

	req := msg.(*Request)
	req.SetTransport("UDP")
	req.SetSource(receiverActualAddr)

	// handleRequest should return an error because CSeq is missing.
	err = txl.handleRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CSeq")

	// The request handler should NOT have been called.
	assert.EqualValues(t, 0, atomic.LoadInt32(&handlerCalled))

	// Read the stateless 400 response that should have been sent.
	buf := make([]byte, 4096)
	receiverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, readErr := receiverConn.ReadFromUDP(buf)
	require.NoError(t, readErr, "expected to receive a stateless 400 response")

	respMsg, err := ParseMessage(buf[:n])
	require.NoError(t, err)

	resp, ok := respMsg.(*Response)
	require.True(t, ok, "expected a SIP response")
	assert.Equal(t, 400, resp.StatusCode)
	assert.Equal(t, "Bad Request", resp.Reason)
}

func TestTransactionLayerClientTx(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	txl := NewTransactionLayer(tp)

	req := testCreateRequest(t, "OPTIONS", "sip:127.0.0.1:9876", "UDP", "127.0.0.1:15070")

	wg := sync.WaitGroup{}
	wg.Add(3)
	var count int32
	for range []int{0, 1, 2} {
		go func() {
			defer wg.Done()
			tx, err := txl.Request(context.TODO(), req)
			if err != nil {
				t.Log("Request failed with err", err)
				return
			}
			atomic.AddInt32(&count, 1)
			require.Equal(t, req, tx.origin)
		}()
	}

	wg.Wait()
	// Only one transaction will be created and executed
	require.EqualValues(t, 1, atomic.LoadInt32(&count))
	require.Equal(t, 2, tp.udp.pool.Size())
	assert.True(t, tp.udp.pool.Get("127.0.0.1:9876") != nil)
}

// A server transaction is created outside the transaction store's lock.
//
// Creating one acquires the connection its responses will ride, and when the
// connection the request arrived on is gone that is a dial to the Via host,
// which to a peer that has gone away lasts the whole connect timeout. Every
// request the layer receives waits on the same lock, so a dial held under it
// stalls the entire server side — and the requests queued behind it time out
// at their senders, whose closed connections make them the next dials. This
// pins the dial open and checks that an unrelated request still gets through.
func TestTransactionLayerServerTxCreationDoesNotHoldTheStore(t *testing.T) {
	receiverAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	receiverConn, err := net.ListenUDP("udp", receiverAddr)
	require.NoError(t, err)
	defer receiverConn.Close()
	receiverActualAddr := receiverConn.LocalAddr().String()

	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	dialing := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	tp.tcp.DialerCreate = func(laddr net.Addr) net.Dialer {
		return net.Dialer{
			LocalAddr: laddr,
			Control: func(_, _ string, _ syscall.RawConn) error {
				once.Do(func() { close(dialing) })
				<-release
				return errors.New("the peer is gone")
			},
		}
	}
	txl := NewTransactionLayer(tp)
	handled := make(chan string, 2)
	txl.OnRequest(func(req *Request, tx *ServerTx) {
		handled <- req.Method.String()
	})

	// A UDP connection in the pool, so the second request needs no dial.
	_, err = tp.udp.CreateConnection(
		context.TODO(),
		testCreateAddr(t, "127.0.0.1:15073"),
		testCreateAddr(t, receiverActualAddr),
		tp.handleMessage,
	)
	require.NoError(t, err)

	// A TCP request whose arrival connection no longer exists: the transport
	// falls back to dialing its Via host, and the dialer above never comes back
	// until released.
	gone := testCreateRequest(t, "OPTIONS", "sip:bob@127.0.0.1", "TCP", "127.0.0.1:15099")
	gone.SetTransport("TCP")
	gone.SetSource("127.0.0.1:15099")
	slow := make(chan error, 1)
	go func() { slow <- txl.handleRequest(gone) }()
	select {
	case <-dialing:
	case <-time.After(2 * time.Second):
		t.Fatal("the request with no connection never reached the dialer")
	}

	// While that dial is pending, a request over the pooled UDP connection
	// must still be handled.
	quick := testCreateRequest(t, "INFO", "sip:bob@127.0.0.1", "UDP", receiverActualAddr)
	quick.SetTransport("UDP")
	quick.SetSource(receiverActualAddr)
	fast := make(chan error, 1)
	go func() { fast <- txl.handleRequest(quick) }()
	select {
	case err := <-fast:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("a request with a live connection waited behind another's dial")
	}
	select {
	case method := <-handled:
		assert.Equal(t, "INFO", method)
	case <-time.After(2 * time.Second):
		t.Fatal("the request over the live connection was never handed to the handler")
	}

	close(release)
	select {
	case err := <-slow:
		require.Error(t, err, "the dial was made to fail, and the transaction with it")
	case <-time.After(2 * time.Second):
		t.Fatal("the released dial never returned")
	}
}

// Two copies of one request racing into the layer end as one transaction: the
// copy that loses the race releases what it created and is received by the
// winner, as a retransmission would be.
func TestTransactionLayerRacingCopiesShareOneServerTx(t *testing.T) {
	receiverAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	receiverConn, err := net.ListenUDP("udp", receiverAddr)
	require.NoError(t, err)
	defer receiverConn.Close()
	receiverActualAddr := receiverConn.LocalAddr().String()

	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	txl := NewTransactionLayer(tp)
	var handled int32
	txl.OnRequest(func(req *Request, tx *ServerTx) {
		atomic.AddInt32(&handled, 1)
		// Answer, so that the transaction finishes on its own.
		require.NoError(t, tx.Respond(NewResponseFromRequest(req, 200, "OK", nil)))
	})
	_, err = tp.udp.CreateConnection(
		context.TODO(),
		testCreateAddr(t, "127.0.0.1:15074"),
		testCreateAddr(t, receiverActualAddr),
		tp.handleMessage,
	)
	require.NoError(t, err)

	req := testCreateRequest(t, "OPTIONS", "sip:bob@127.0.0.1", "UDP", receiverActualAddr)
	req.SetTransport("UDP")
	req.SetSource(receiverActualAddr)

	const copies = 8
	var wg sync.WaitGroup
	errs := make(chan error, copies)
	for range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- txl.handleRequest(req)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, atomic.LoadInt32(&handled), "one transaction, one handler call")
	key, err := ServerTxKeyMake(req)
	require.NoError(t, err)
	_, exists := txl.serverTransactions.get(key)
	assert.True(t, exists, "the winner is in the store")
}
