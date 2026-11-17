package sipgo

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

// A private Via and a different UDP source port reproduce the Cisco gateway
// behind NAT: without the fallback, its challenge is sent to the Via's port.
func rportFallbackInvite() *sip.Request {
	req := sip.NewRequest(sip.INVITE, sip.Uri{Scheme: "sip", User: "1001", Host: "example.com"})
	req.AppendHeader(&sip.ViaHeader{
		ProtocolName: "SIP", ProtocolVersion: "2.0", Transport: "UDP",
		Host: "10.20.0.10", Port: 5060,
		Params: sip.HeaderParams{{K: "branch", V: "z9hG4bK-rport-fallback"}},
	})
	req.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{Scheme: "sip", User: "caller", Host: "example.com"},
		Params:  sip.HeaderParams{{K: "tag", V: "caller"}},
	})
	req.AppendHeader(&sip.ToHeader{Address: req.Recipient})
	req.AppendHeader(sip.NewHeader("Call-ID", "rport-fallback@example.com"))
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 101, MethodName: sip.INVITE})
	req.SetBody(nil)

	return req
}

func newRportFallbackServer(t *testing.T, invite RequestHandler) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()

	ua, err := NewUA()
	require.NoError(t, err)
	var handlers sync.WaitGroup
	t.Cleanup(func() {
		require.NoError(t, ua.Close())
		handlers.Wait()
	})

	srv, err := NewServer(ua)
	require.NoError(t, err)

	if invite != nil {
		srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
			handlers.Add(1)
			defer handlers.Done()

			invite(req, tx)
		})
	}

	listen, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	serveDone := make(chan error, 1)

	go func() { serveDone <- srv.ServeUDP(listen) }()

	t.Cleanup(func() {
		require.NoError(t, listen.Close())
		require.NoError(t, <-serveDone)
	})

	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, peer.Close()) })

	return peer, listen.LocalAddr().(*net.UDPAddr)
}

func readRportFallbackResponse(t *testing.T, peer *net.UDPConn, server *net.UDPAddr, code int) *sip.Response {
	t.Helper()

	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 4096)
	n, addr, err := peer.ReadFromUDP(buf)
	require.NoError(t, err)
	require.Equal(t, server.String(), addr.String(), "responses must use the receiving socket")

	msg, err := sip.ParseMessage(buf[:n])
	require.NoError(t, err)
	res, ok := msg.(*sip.Response)
	require.True(t, ok)
	require.Equal(t, code, res.StatusCode)

	host, port, err := net.SplitHostPort(peer.LocalAddr().String())
	require.NoError(t, err)
	require.Equal(t, host, res.Via().Params.GetOr("received", ""))
	require.Equal(t, port, res.Via().Params.GetOr("rport", ""))

	return res
}

func TestServerRportFallbackChallengeRetransmission(t *testing.T) {
	respond := make(chan struct{})
	release := sync.OnceFunc(func() { close(respond) })
	errs := make(chan error, 1)
	var calls atomic.Int32

	peer, addr := newRportFallbackServer(t, func(req *sip.Request, tx sip.ServerTransaction) {
		calls.Add(1)
		<-respond
		res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil)
		res.AppendHeader(sip.NewHeader("WWW-Authenticate", `Digest realm="example.com", nonce="test", algorithm=MD5, qop="auth"`))
		errs <- tx.Respond(res)
	})
	t.Cleanup(release)

	req := rportFallbackInvite()
	_, err := peer.WriteToUDP([]byte(req.String()), addr)
	require.NoError(t, err)

	// The transaction's automatic 100 must work before application code replies.
	readRportFallbackResponse(t, peer, addr, sip.StatusTrying)
	release()
	first := readRportFallbackResponse(t, peer, addr, sip.StatusUnauthorized)
	require.NoError(t, <-errs)

	// Withhold the ACK so Timer G retransmits the cached challenge.
	retransmit := readRportFallbackResponse(t, peer, addr, sip.StatusUnauthorized)
	require.Equal(t, first.String(), retransmit.String())

	// A duplicate INVITE must receive the same challenge, not run the handler again.
	_, err = peer.WriteToUDP([]byte(req.String()), addr)
	require.NoError(t, err)
	retransmit = readRportFallbackResponse(t, peer, addr, sip.StatusUnauthorized)
	require.Equal(t, first.String(), retransmit.String())
	require.EqualValues(t, 1, calls.Load())
}

func TestServerRportFallbackCancel(t *testing.T) {
	finish := make(chan struct{})
	release := sync.OnceFunc(func() { close(finish) })

	peer, addr := newRportFallbackServer(t, func(req *sip.Request, tx sip.ServerTransaction) {
		<-finish
	})
	t.Cleanup(release)

	req := rportFallbackInvite()
	_, err := peer.WriteToUDP([]byte(req.String()), addr)
	require.NoError(t, err)
	readRportFallbackResponse(t, peer, addr, sip.StatusTrying)

	cancel := req.Clone()
	cancel.Method = sip.CANCEL
	cancel.CSeq().MethodName = sip.CANCEL
	_, err = peer.WriteToUDP([]byte(cancel.String()), addr)
	require.NoError(t, err)

	// CANCEL is answered by sipgo without reaching the application's middleware.
	readRportFallbackResponse(t, peer, addr, sip.StatusOK)
	readRportFallbackResponse(t, peer, addr, sip.StatusRequestTerminated)
	release()
}

func TestServerRportFallbackStatelessError(t *testing.T) {
	peer, addr := newRportFallbackServer(t, nil)
	req := rportFallbackInvite()
	req.RemoveHeader("CSeq")

	_, err := peer.WriteToUDP([]byte(req.String()), addr)
	require.NoError(t, err)
	readRportFallbackResponse(t, peer, addr, sip.StatusBadRequest)
}
