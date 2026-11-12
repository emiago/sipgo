package sipgo

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

type testDialogRequester struct {
	transactionRequest func(context.Context, *sip.Request, ...DialogRequestOption) (sip.ClientTransaction, error)
	do                 func(context.Context, *sip.Request, ...DialogRequestOption) (*sip.Response, error)
	writeBye           func(context.Context, *sip.Request, ...DialogRequestOption) error
	state              func() sip.DialogState
}

// testDialogRequestSetup returns a confirmed TLS dialog with a loose route through proxy.example.
func testDialogRequestSetup(t *testing.T) (*sip.Request, *sip.Response) {
	invite, _, _ := createTestInvite(t, "sip:uas@uas.com", "TLS", "uas.com:5061")
	invite.SetTransport("TLS")
	invite.SetSource("198.51.100.10:5061")
	invite.AppendHeader(sip.NewHeader("Contact", "<sip:uac@uac.com:5061>"))
	invite.AppendHeader(sip.NewHeader("Record-Route", "<sip:proxy.example:5060;lr>"))
	// The answer copies Record-Route from the INVITE.
	answer := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	answer.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.com:5061>"))
	answer.SetSource("198.51.100.10:5061")
	return invite, answer
}

func testDialogRequestSession(t *testing.T, clientSide bool, client *Client, invite *sip.Request, answer *sip.Response, rewriteContact bool) testDialogRequester {
	ua := &DialogUA{Client: client, RewriteContact: rewriteContact}
	if clientSide {
		inviteTx, err := client.TransactionRequest(context.Background(), invite.Clone())
		require.NoError(t, err)
		session := &DialogClientSession{UA: ua, inviteTx: inviteTx}
		session.InviteRequest, session.InviteResponse = invite, answer
		session.InitWithState(sip.DialogStateConfirmed)
		return testDialogRequester{session.TransactionRequestWithOptions, session.DoWithOptions, session.WriteByeWithOptions, session.LoadState}
	}
	session := &DialogServerSession{ua: ua, inviteTx: sip.NewServerTx("invite", invite, nil, slog.Default())}
	session.InviteRequest, session.InviteResponse = invite, answer
	session.InitWithState(sip.DialogStateConfirmed)
	return testDialogRequester{session.TransactionRequestWithOptions, session.DoWithOptions, session.WriteByeWithOptions, session.LoadState}
}

func TestDialogRequestTransport(t *testing.T) {
	for _, clientSide := range []bool{false, true} {
		side := map[bool]string{true: "client", false: "server"}[clientSide]
		for _, method := range []sip.RequestMethod{sip.BYE, sip.REFER} {
			for _, transport := range []string{"", "udp"} {
				t.Run(method.String()+"/"+side+"/"+transport, func(t *testing.T) {
					var sent *sip.Request
					client := testClient(t, func(req *sip.Request) *sip.Response {
						sent = req.Clone()
						return sip.NewResponseFromRequest(req, 200, "OK", nil)
					})
					defer client.UserAgent.Close()
					invite, answer := testDialogRequestSetup(t)
					session := testDialogRequestSession(t, clientSide, client, invite, answer, false)

					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					req := sip.NewRequest(method, sip.Uri{Scheme: "sip", Host: "peer.example", Port: 5060})
					var options []DialogRequestOption
					expectedTransport := "TLS"
					if transport != "" {
						options = []DialogRequestOption{WithDialogRequestTransport(transport)}
						expectedTransport = "UDP"
					}
					if method == sip.BYE {
						require.NoError(t, session.writeBye(ctx, req, options...))
					} else {
						_, err := session.do(ctx, req, options...)
						require.NoError(t, err)
					}

					require.NotNil(t, sent)
					require.Equal(t, expectedTransport, sent.Transport())
					require.Len(t, sent.GetHeaders("Via"), 1)
					require.Equal(t, expectedTransport, sent.Via().Transport)
					require.Equal(t, 5060, sent.Recipient.Port)
					require.Equal(t, invite.CallID().Value(), sent.CallID().Value())
					require.NotNil(t, sent.Route())
					require.Equal(t, "proxy.example:5060", sent.Destination())
					require.Equal(t, method, sent.CSeq().MethodName)
					require.Equal(t, "TLS", invite.Transport())
					if method == sip.BYE {
						require.Equal(t, sip.DialogStateEnded, session.state())
					} else {
						require.Equal(t, sip.DialogStateConfirmed, session.state())
					}
				})
			}
		}
	}
}

func TestDialogRequestTransportDestination(t *testing.T) {
	for _, clientSide := range []bool{false, true} {
		t.Run(map[bool]string{true: "client", false: "server"}[clientSide], func(t *testing.T) {
			var sent []*sip.Request
			client := testClient(t, func(req *sip.Request) *sip.Response {
				if !req.IsInvite() {
					sent = append(sent, req.Clone())
				}
				return sip.NewResponseFromRequest(req, 200, "OK", nil)
			})
			defer client.UserAgent.Close()
			invite, answer := testDialogRequestSetup(t)
			for invite.RemoveHeader("Record-Route") {
			}
			for answer.RemoveHeader("Record-Route") {
			}
			// RewriteContact targets the TLS source address.
			session := testDialogRequestSession(t, clientSide, client, invite, answer, true)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			target := sip.Uri{Scheme: "sip", Host: "peer.example", Port: 5060}
			_, err := session.do(ctx, sip.NewRequest(sip.INFO, target))
			require.NoError(t, err)
			_, err = session.do(ctx, sip.NewRequest(sip.INFO, target), WithDialogRequestTransport("UDP"))
			require.NoError(t, err)
			explicit := sip.NewRequest(sip.INFO, target)
			explicit.SetDestination("203.0.113.5:5070")
			_, err = session.do(ctx, explicit, WithDialogRequestTransport("UDP"))
			require.NoError(t, err)

			require.Len(t, sent, 3)
			require.Equal(t, "198.51.100.10:5061", sent[0].Destination())
			require.Equal(t, "peer.example:5060", sent[1].Destination())
			require.Equal(t, "203.0.113.5:5070", sent[2].Destination())
			require.Equal(t, sent[0].CSeq().SeqNo+1, sent[1].CSeq().SeqNo)
		})
	}
}

func TestDialogRequestTransportRejected(t *testing.T) {
	for _, clientSide := range []bool{false, true} {
		t.Run(map[bool]string{true: "client", false: "server"}[clientSide], func(t *testing.T) {
			sent := 0
			client := testClient(t, func(req *sip.Request) *sip.Response {
				if !req.IsInvite() {
					sent++
				}
				return sip.NewResponseFromRequest(req, 200, "OK", nil)
			})
			defer client.UserAgent.Close()
			invite, answer := testDialogRequestSetup(t)
			session := testDialogRequestSession(t, clientSide, client, invite, answer, false)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			target := sip.Uri{Scheme: "sip", Host: "peer.example", Port: 5060}
			_, err := session.transactionRequest(ctx, sip.NewRequest(sip.INFO, target), WithDialogRequestTransport("sctp"))
			require.Error(t, err)
			secureTarget := target
			secureTarget.Scheme = "sips"
			require.Error(t, session.writeBye(ctx, sip.NewRequest(sip.BYE, secureTarget), WithDialogRequestTransport("UDP")))
			require.Equal(t, 0, sent)
			require.Equal(t, sip.DialogStateConfirmed, session.state())
		})
	}
}

func TestDialogRequestTransportSecurity(t *testing.T) {
	for _, location := range []string{"invite", "target", "invite contact", "answer contact", "request route", "invite route", "answer route"} {
		t.Run(location, func(t *testing.T) {
			invite := sip.NewRequest(sip.INVITE, sip.Uri{Scheme: "sip", Host: "peer"})
			req := sip.NewRequest(sip.BYE, invite.Recipient)
			answer := sip.NewResponse(200, "OK")
			switch location {
			case "invite":
				invite.Recipient.Scheme = "sips"
			case "target":
				req.Recipient.Scheme = "sips"
			case "invite contact":
				invite.AppendHeader(sip.NewHeader("Contact", "<sips:uac@peer>"))
			case "answer contact":
				answer.AppendHeader(sip.NewHeader("Contact", "<SIPS:uas@peer>"))
			case "request route":
				req.AppendHeader(sip.NewHeader("Route", "<sips:proxy;lr>"))
			case "invite route":
				invite.AppendHeader(sip.NewHeader("Record-Route", "<sip:first;lr>, <sips:proxy;lr>"))
			case "answer route":
				answer.AppendHeader(sip.NewHeader("Record-Route", "<sips:proxy;lr>"))
			}
			for _, transport := range []string{"UDP", "TCP", "WS"} {
				require.Error(t, validateDialogRequestOptions(invite, answer, req, []DialogRequestOption{WithDialogRequestTransport(transport)}))
			}
			for _, transport := range []string{"TLS", "wss"} {
				require.NoError(t, validateDialogRequestOptions(invite, answer, req, []DialogRequestOption{WithDialogRequestTransport(transport)}))
			}
			require.NoError(t, validateDialogRequestOptions(invite, answer, req, nil))
		})
	}

	t.Run("nil answer", func(t *testing.T) {
		invite := sip.NewRequest(sip.INVITE, sip.Uri{Scheme: "sip", Host: "peer"})
		req := sip.NewRequest(sip.BYE, invite.Recipient)
		require.NoError(t, validateDialogRequestOptions(invite, nil, req, []DialogRequestOption{WithDialogRequestTransport("UDP")}))
	})
}
