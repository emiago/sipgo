package sip

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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

func TestTransactionLayerCSeqMethodMismatch(t *testing.T) {
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
	localAddr := "127.0.0.1:15072"
	_, err = tp.udp.CreateConnection(
		context.TODO(),
		testCreateAddr(t, localAddr),
		testCreateAddr(t, receiverActualAddr),
		tp.handleMessage,
	)
	require.NoError(t, err)

	// Build a malformed request: CSeq method mismatch method in Request Line.
	raw := strings.Join([]string{
		"REGISTER sip:192.168.100.30:5060 SIP/2.0",
		"Via: SIP/2.0/UDP " + receiverActualAddr + ";branch=z9hG4bK-test123",
		"From: <sip:alice@example.com>;tag=from1",
		"To: <sip:alice@example.com>",
		"Call-ID: malformed-test-call-id",
		"Content-Length: 0",
		"Cseq: 1 INVITE", // INVITE method for REGISTER request
		"",
		"",
	}, "\r\n")

	msg, err := ParseMessage([]byte(raw))
	require.NoError(t, err)

	req := msg.(*Request)
	req.SetTransport("UDP")
	req.SetSource(receiverActualAddr)

	// handleRequest should return an error because CSeq method does not match Request Line method.
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
