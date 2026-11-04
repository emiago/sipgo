package sipgo

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func TestTCPRejectedInviteAckConsumedByTransaction(t *testing.T) {
	ua, err := NewUA()
	require.NoError(t, err)
	defer ua.Close()
	srv, err := NewServer(ua)
	require.NoError(t, err)

	serverReader, serverWriter := io.Pipe()
	clientReader, clientWriter := io.Pipe()
	serverAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	clientAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	client := &fakes.TCPConn{LAddr: clientAddr, RAddr: serverAddr, Reader: clientReader, Writer: serverWriter}
	listener := &fakes.TCPListener{LAddr: serverAddr, Conns: make(chan *fakes.TCPConn, 1)}
	listener.Conns <- &fakes.TCPConn{LAddr: serverAddr, RAddr: clientAddr, Reader: serverReader, Writer: clientWriter}

	txCh := make(chan sip.ServerTransaction, 1)
	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		txCh <- tx
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusRequestPending, "Request Pending", nil))
	})
	ackHandled := make(chan struct{}, 1)
	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) { ackHandled <- struct{}{} })
	go srv.TransportLayer().ServeTCP(listener)

	sender := sip.Uri{User: "alice", Host: clientAddr.IP.String(), Port: clientAddr.Port}
	recipient := sip.Uri{User: "bob", Host: serverAddr.IP.String(), Port: serverAddr.Port}
	invite := createSimpleRequest(sip.INVITE, sender, recipient, "TCP")
	res, err := sip.NewParser().ParseSIP(client.TestRequest(t, []byte(invite.String())))
	require.NoError(t, err)
	require.Equal(t, sip.StatusRequestPending, res.(*sip.Response).StatusCode)

	tx := <-txCh
	select {
	case <-tx.Done():
		t.Fatal("rejected INVITE transaction terminated before ACK")
	case <-time.After(50 * time.Millisecond):
	}

	ack := invite.Clone()
	ack.Method = sip.ACK
	ack.CSeq().MethodName = sip.ACK
	client.TestWriteConn(t, []byte(ack.String()))
	select {
	case <-tx.Done():
	case <-time.After(time.Second):
		t.Fatal("ACK did not complete rejected INVITE transaction")
	}
	select {
	case <-ackHandled:
		t.Fatal("non-2xx ACK reached the ACK request handler")
	case <-time.After(50 * time.Millisecond):
	}
}
