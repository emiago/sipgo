package sipgo

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/siptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClient(t testing.TB, f func(req *sip.Request) *sip.Response) *Client {
	ua, _ := NewUA()
	client, err := NewClient(ua)
	require.NoError(t, err)
	client.TxRequester = &siptest.ClientTxRequester{
		OnRequest: f,
	}
	return client
}

func testClientResponder(t testing.TB, f func(req *sip.Request, w *siptest.ClientTxResponder)) *Client {
	ua, _ := NewUA()
	client, err := NewClient(ua)
	require.NoError(t, err)
	client.TxRequester = &siptest.ClientTxRequesterResponder{
		OnRequest: f,
	}
	return client
}

func TestDialogClientRequestRecordRouteHeaders(t *testing.T) {
	client := testClient(t, func(req *sip.Request) *sip.Response {
		return sip.NewResponseFromRequest(req, 200, "OK", nil)
	})

	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "test", Host: "localhost"})
	invite.AppendHeader(sip.NewHeader("Contact", "<sip:uac@uac.p1.com>"))
	err := clientRequestBuildReq(client, invite)
	require.NoError(t, err)
	// assert.Equal(t, "localhost:5060", invite.Source())
	assert.Equal(t, "localhost:5060", invite.Destination())

	t.Run("LooseRouting", func(t *testing.T) {

		resp := sip.NewResponseFromRequest(invite, 200, "OK", nil)
		resp.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.p2.com>"))
		// Fake some proxy headers
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p2.com;lr>"))
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p1.com;lr>"))

		s := DialogClientSession{
			UA: &DialogUA{
				Client: client,
			},
			Dialog: Dialog{
				InviteRequest:  invite,
				InviteResponse: resp,
			},
			inviteTx: sip.NewClientTx("test", invite, nil, slog.Default()),
		}
		// Send canceled request
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		ack := newAckRequestUAC(s.InviteRequest, s.InviteResponse, nil)
		assert.Equal(t, "uas.p2.com:5060", ack.Destination())
		s.WriteAck(ctx, ack)
		assert.Equal(t, "sip:uas@uas.p2.com", ack.Recipient.String())
		assert.Equal(t, "<sip:p1.com;lr>", ack.Route().Value())
		assert.Equal(t, "<sip:p2.com;lr>", ack.GetHeaders("Route")[1].Value())

		bye := newByeRequestUAC(s.InviteRequest, s.InviteResponse, nil)
		s.Do(ctx, bye)
		assert.Equal(t, "sip:uas@uas.p2.com", bye.Recipient.String())
		assert.Equal(t, "<sip:p1.com;lr>", bye.Route().Value())
		assert.Equal(t, "<sip:p2.com;lr>", bye.GetHeaders("Route")[1].Value())
	})

	t.Run("StrictRouting", func(t *testing.T) {

		resp := sip.NewResponseFromRequest(invite, 200, "OK", nil)
		resp.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.p2.com>"))
		// Fake some proxy headers
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p2.com;lr>"))
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p1.com>"))

		s := DialogClientSession{
			UA: &DialogUA{
				Client: client,
			},
			Dialog: Dialog{
				InviteRequest:  invite,
				InviteResponse: resp,
			},
			inviteTx: sip.NewClientTx("test", invite, nil, slog.Default()),
		}

		// Send canceled request
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		ack := newAckRequestUAC(s.InviteRequest, s.InviteResponse, nil)
		assert.Equal(t, "uas.p2.com:5060", ack.Destination())
		s.WriteAck(ctx, ack)
		assert.Equal(t, "sip:p1.com", ack.Recipient.String())
		assert.Equal(t, "<sip:p1.com>", ack.Route().Value())
		assert.Equal(t, "<sip:p2.com;lr>", ack.GetHeaders("Route")[1].Value())

		bye := newByeRequestUAC(s.InviteRequest, s.InviteResponse, nil)
		s.Do(ctx, bye)
		assert.Equal(t, "sip:p1.com", bye.Recipient.String())
		assert.Equal(t, "<sip:p1.com>", bye.Route().Value())
		assert.Equal(t, "<sip:p2.com;lr>", bye.GetHeaders("Route")[1].Value())
	})

}

func TestDialogClientMultiRequest(t *testing.T) {
	var sentReq *sip.Request
	client := testClient(t, func(req *sip.Request) *sip.Response {
		sentReq = req
		return sip.NewResponseFromRequest(req, 200, "OK", nil)
	})

	dua := DialogUA{
		Client: client,
	}
	d, err := dua.Invite(context.TODO(), sip.Uri{User: "test", Host: "localhost"}, nil)
	require.NoError(t, err)
	assert.NotNil(t, d.InviteRequest.From())
	assert.NotNil(t, d.InviteRequest.To())
	assert.NotNil(t, d.InviteRequest.Contact())
	assert.NotEmpty(t, d.InviteRequest.CallID())
	assert.NotEmpty(t, d.InviteRequest.MaxForwards())

	err = d.WaitAnswer(context.TODO(), AnswerOptions{})
	require.NoError(t, err)
	d.Ack(context.TODO())
	assert.Equal(t, d.InviteRequest.CSeq().SeqNo, sentReq.CSeq().SeqNo)

	_, err = d.Do(context.Background(), sip.NewRequest(sip.INVITE, sip.Uri{User: "reinvite", Host: "localhost"}))
	require.NoError(t, err)

	assert.Equal(t, d.InviteRequest.CSeq().SeqNo+1, sentReq.CSeq().SeqNo)
}

func TestDialogClientMultiResponses(t *testing.T) {

	t.Run("ProvisionalLoop", func(t *testing.T) {
		client := testClient(t, func(req *sip.Request) *sip.Response {
			return sip.NewResponseFromRequest(req, 100, "Trying", nil)
		})

		dua := DialogUA{
			Client: client,
		}
		d, err := dua.Invite(context.TODO(), sip.Uri{User: "test", Host: "localhost"}, nil)
		require.NoError(t, err)
		go func() {
			// Receive more provisional
			for i := 0; i < 10; i++ {
				d.inviteTx.(*sip.ClientTx).Receive(sip.NewResponseFromRequest(d.InviteRequest, 100, "Trying", nil))
			}
		}()
		err = d.WaitAnswer(context.TODO(), AnswerOptions{})
		require.Error(t, err)
	})
	t.Run("ProxyAuthLoop", func(t *testing.T) {
		var sentReq *sip.Request
		client := testClient(t, func(req *sip.Request) *sip.Response {
			sentReq = req
			res := sip.NewResponseFromRequest(req, 407, "Unauthorized", nil)
			challenge := `Digest username="user", realm="test", nonce="662d65a084b88c6d2a745a9de086fa91", uri="sip:+user@example.com", algorithm=sha-256, response="3681b63e5d9c3bb80e5350e2783d7b88"`
			res.AppendHeader(sip.NewHeader("Proxy-Authenticate", challenge))
			return res
		})

		dua := DialogUA{
			Client: client,
		}
		d, err := dua.Invite(context.TODO(), sip.Uri{User: "test", Host: "localhost"}, nil)
		require.NoError(t, err)

		err = d.WaitAnswer(context.TODO(), AnswerOptions{Password: "secret"})
		require.Error(t, err)
		assert.Equal(t, d.InviteRequest.CSeq().SeqNo, sentReq.CSeq().SeqNo)
	})

	t.Run("AuthLoop", func(t *testing.T) {
		var sentReq *sip.Request
		client := testClient(t, func(req *sip.Request) *sip.Response {
			sentReq = req
			res := sip.NewResponseFromRequest(req, 401, "Unauthorized", nil)
			challenge := `Digest username="user", realm="test", nonce="662d65a084b88c6d2a745a9de086fa91", uri="sip:+user@example.com", algorithm=sha-256, response="3681b63e5d9c3bb80e5350e2783d7b88"`
			res.AppendHeader(sip.NewHeader("WWW-Authenticate", challenge))
			return res
		})

		dua := DialogUA{
			Client: client,
		}
		d, err := dua.Invite(context.TODO(), sip.Uri{User: "test", Host: "localhost"}, nil)
		require.NoError(t, err)

		err = d.WaitAnswer(context.TODO(), AnswerOptions{Password: "secret"})
		require.Error(t, err)
		assert.Equal(t, d.InviteRequest.CSeq().SeqNo, sentReq.CSeq().SeqNo)
	})

}

func TestDialogClientACKRetransmission(t *testing.T) {
	var acks int32
	client := testClientResponder(t, func(req *sip.Request, w *siptest.ClientTxResponder) {
		if req.IsAck() {
			atomic.AddInt32(&acks, 1)
			return
		}

		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		w.Receive(res)
		time.Sleep(sip.T1)
		w.Receive(res)
		time.Sleep(sip.T1)
		w.Receive(res)
	})

	dua := DialogUA{
		Client: client,
	}
	d, err := dua.Invite(context.TODO(), sip.Uri{User: "test", Host: "localhost"}, nil)
	require.NoError(t, err)
	err = d.WaitAnswer(context.TODO(), AnswerOptions{})
	require.NoError(t, err)

	// We will keep receiving retransmission
	if err := d.Ack(context.TODO()); err != nil {
		t.Error(err)
	}
	time.Sleep(4 * sip.T1)
	// It should retransmit
	state := d.LoadState()
	assert.Equal(t, sip.DialogStateConfirmed, state)
	assert.EqualValues(t, 3, atomic.LoadInt32(&acks))
}

func BenchmarkDialogDo(b *testing.B) {
	ua, _ := NewUA()
	cli, _ := NewClient(ua)
	cli.TxRequester = &siptest.ClientTxRequester{
		OnRequest: func(req *sip.Request) *sip.Response {
			return sip.NewResponseFromRequest(req, 200, "OK", nil)
		},
	}
	dua := &DialogUA{
		Client: cli,
	}

	dialog, err := dua.Invite(context.TODO(), sip.Uri{User: "test", Host: "localhost"}, nil)
	require.NoError(b, err)
	dialog.WaitAnswer(context.TODO(), AnswerOptions{})

	b.Run("ACK", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dialog.Ack(context.TODO())
		}
	})
	b.Run("NotSupported", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			req := sip.NewRequest(sip.REFER, sip.Uri{User: "refer", Host: "localhost"})
			dialog.Do(context.TODO(), req)
		}
	})

}
