package sipgo

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RetellAI/sipgo/sip"
	"github.com/stretchr/testify/require"
)

type ackReplayTransaction struct {
	sip.ClientTransaction
	replay sip.FnTxResponse
}

func (tx *ackReplayTransaction) OnRetransmission(f sip.FnTxResponse) bool {
	tx.replay = f
	return true
}

type ackRequestWriter func(*sip.Request) error

func (w ackRequestWriter) Request(_ context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	return nil, w(req)
}

func TestDialogClientACKRetransmissionRouting(t *testing.T) {
	for _, tc := range []struct {
		name        string
		transport   string
		routes      []string
		destination string
		rewrite     bool
		want        string
	}{
		{name: "direct", transport: "TCP", want: "192.0.2.20:5060"},
		{name: "loose TCP", transport: "TCP", routes: []string{"<sip:192.0.2.2;lr>", "<sip:192.0.2.1;transport=tcp;lr>"}, want: "192.0.2.1:5060"},
		{name: "loose UDP", transport: "UDP", routes: []string{"<sip:192.0.2.2;lr>", "<sip:192.0.2.1;lr>"}, want: "192.0.2.1:5060"},
		{name: "strict", transport: "TCP", routes: []string{"<sip:192.0.2.2;lr>", "<sip:192.0.2.1>"}, want: "192.0.2.1:5060"},
		{name: "explicit destination", transport: "TCP", routes: []string{"<sip:192.0.2.1;lr>"}, destination: "198.51.100.50:5090", want: "198.51.100.50:5090"},
		{name: "rewritten contact", transport: "TCP", rewrite: true, want: "192.0.2.30:5090"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ua, err := NewUA()
			require.NoError(t, err)
			t.Cleanup(func() { _ = ua.Close() })
			client, err := NewClient(ua)
			require.NoError(t, err)
			var mu sync.Mutex
			var sent []*sip.Request
			client.TxRequester = ackRequestWriter(func(req *sip.Request) error {
				mu.Lock()
				defer mu.Unlock()
				sent = append(sent, req)
				return nil
			})
			invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "user", Host: "192.0.2.20", Port: 5060})
			invite.SetTransport(tc.transport)
			require.NoError(t, clientRequestBuildReq(client, invite))
			res := sip.NewResponseFromRequest(invite, 200, "OK", nil)
			res.AppendHeader(sip.NewHeader("Contact", "<sip:192.0.2.20:5060>"))
			res.SetSource("192.0.2.30:5090")
			for _, route := range tc.routes {
				res.AppendHeader(sip.NewHeader("Record-Route", route))
			}
			tx := &ackReplayTransaction{}
			d := &DialogClientSession{
				UA:       &DialogUA{Client: client, RewriteContact: tc.rewrite},
				Dialog:   Dialog{InviteRequest: invite, InviteResponse: res},
				inviteTx: tx,
			}
			d.Init()
			d.lastCSeqNo.Store(invite.CSeq().SeqNo)
			ack := newAckRequestUAC(invite, res, []byte("answer"))
			ack.SetDestination(tc.destination)
			ack.AppendHeader(sip.NewHeader("X-Test", "preserved"))
			require.NoError(t, d.WriteAck(context.Background(), ack))
			require.Len(t, sent, 1)
			require.Equal(t, tc.want, sent[0].Destination())
			d.lastCSeqNo.Add(1)

			const retries = 8
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < retries; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					tx.replay(res)
				}()
			}
			close(start)
			wg.Wait()
			require.Len(t, sent, retries+1)
			for _, req := range sent {
				require.Equal(t, tc.want, req.Destination())
				require.Equal(t, sent[0].Recipient.String(), req.Recipient.String())
				require.Equal(t, invite.CSeq().SeqNo, req.CSeq().SeqNo)
				require.Equal(t, sip.ACK, req.CSeq().MethodName)
				require.Len(t, req.GetHeaders("Via"), 1)
				require.Equal(t, sent[0].Via().Value(), req.Via().Value())
				require.Len(t, req.GetHeaders("Route"), len(tc.routes))
				for i, route := range req.GetHeaders("Route") {
					require.Equal(t, tc.routes[len(tc.routes)-1-i], route.Value())
				}
				require.Equal(t, "preserved", req.GetHeader("X-Test").Value())
				require.Equal(t, []byte("answer"), req.Body())
				require.Equal(t, tc.transport, req.Transport())
			}
			for i := 1; i < len(sent); i++ {
				require.NotSame(t, sent[0], sent[i])
			}
		})
	}
}

func TestDialogClientACKRetransmissionWriteFailure(t *testing.T) {
	ua, err := NewUA()
	require.NoError(t, err)
	t.Cleanup(func() { _ = ua.Close() })
	client, err := NewClient(ua)
	require.NoError(t, err)
	writes := 0
	failure := errors.New("transport failed")
	client.TxRequester = ackRequestWriter(func(req *sip.Request) error {
		writes++
		if writes > 1 {
			return failure
		}
		return nil
	})
	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "user", Host: "192.0.2.20"})
	require.NoError(t, clientRequestBuildReq(client, invite))
	res := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	tx := &ackReplayTransaction{}
	d := &DialogClientSession{UA: &DialogUA{Client: client}, Dialog: Dialog{InviteRequest: invite, InviteResponse: res}, inviteTx: tx}
	d.Init()
	d.lastCSeqNo.Store(invite.CSeq().SeqNo)
	require.NoError(t, d.Ack(context.Background()))
	tx.replay(res)
	require.Equal(t, sip.DialogStateEnded, d.LoadState())
	require.ErrorIs(t, context.Cause(d.Context()), failure)
}
