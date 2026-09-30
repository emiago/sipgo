package sipgo

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/RetellAI/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func TestDialogClientACKRetransmissionTCPProxy(t *testing.T) {
	for _, auth := range []bool{false, true} {
		t.Run(fmt.Sprintf("authenticate=%t", auth), func(t *testing.T) {
			proxy, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = proxy.Close() })
			contact, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = contact.Close() })
			serverUA, err := NewUA()
			require.NoError(t, err)
			t.Cleanup(func() { _ = serverUA.Close() })
			server, err := NewServer(serverUA)
			require.NoError(t, err)
			answers := make(chan *sip.Response, 1)
			acks := make(chan *sip.Request, 8)
			server.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
				if auth && req.GetHeader("Proxy-Authorization") == nil {
					res := sip.NewResponseFromRequest(req, 407, "Proxy Authentication Required", nil)
					res.AppendHeader(sip.NewHeader("Proxy-Authenticate", `Digest realm="test", nonce="nonce", algorithm=MD5`))
					if err := tx.Respond(res); err != nil {
						t.Errorf("challenge: %v", err)
					}
					return
				}
				res := sip.NewResponseFromRequest(req, 200, "OK", nil)
				res.AppendHeader(sip.NewHeader("Contact", "<sip:"+contact.Addr().String()+">"))
				res.AppendHeader(sip.NewHeader("Record-Route", "<sip:"+proxy.Addr().String()+";transport=tcp;lr>"))
				if err := tx.Respond(res); err != nil {
					t.Errorf("answer: %v", err)
				}
				answers <- res
			})
			server.OnAck(func(req *sip.Request, _ sip.ServerTransaction) {
				if req.Recipient.Host == "carrier.invalid" {
					return
				}
				acks <- req
			})
			served := make(chan struct{})
			go func() {
				defer close(served)
				_ = server.ServeTCP(proxy)
			}()
			t.Cleanup(func() {
				_ = proxy.Close()
				<-served
			})
			clientUA, err := NewUA()
			require.NoError(t, err)
			t.Cleanup(func() { _ = clientUA.Close() })
			client, err := NewClient(clientUA)
			require.NoError(t, err)
			dua := DialogUA{Client: client}
			req := sip.NewRequest(sip.INVITE, sip.Uri{Scheme: "sip", User: "user", Host: "carrier.invalid"})
			req.SetTransport("TCP")
			req.SetDestination(proxy.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d, err := dua.WriteInvite(ctx, req)
			require.NoError(t, err)
			t.Cleanup(func() {
				d.inviteTx.Terminate()
				_ = d.Close()
			})
			require.NoError(t, d.WaitAnswer(ctx, AnswerOptions{Username: "user", Password: "password"}))
			require.NoError(t, d.Ack(ctx))
			var answer *sip.Response
			select {
			case answer = <-answers:
			case <-ctx.Done():
				t.Fatal("missing proxy answer")
			}
			for i := 0; i < 3; i++ {
				if i > 0 {
					require.NoError(t, server.WriteResponse(answer.Clone()))
				}
				select {
				case ack := <-acks:
					require.Equal(t, "sip:"+contact.Addr().String(), ack.Recipient.String())
					require.Equal(t, req.CSeq().SeqNo, ack.CSeq().SeqNo)
					require.Len(t, ack.GetHeaders("Route"), 1)
					require.Equal(t, proxy.Addr().String(), ack.Route().Address.Host+fmt.Sprintf(":%d", ack.Route().Address.Port))
				case <-ctx.Done():
					t.Fatalf("ACK %d did not reach the proxy", i+1)
				}
			}
		})
	}
}
