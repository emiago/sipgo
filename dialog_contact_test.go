package sipgo

import (
	"context"
	"log/slog"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dialog's default Contact goes only on requests that may carry one:
// RFC 3261 Table 2 rules it out of BYE and CANCEL, RFC 3428 out of
// MESSAGE, RFC 3262 out of PRACK and RFC 6086 out of INFO.
var dialogContactCases = []struct {
	method  sip.RequestMethod
	contact bool
}{
	{sip.BYE, false},
	{sip.CANCEL, false},
	{sip.MESSAGE, false},
	{sip.PRACK, false},
	{sip.INFO, false},
	{sip.INVITE, true},
	{sip.UPDATE, true},
	{sip.REFER, true},
	{sip.NOTIFY, true},
	{sip.OPTIONS, true},
}

func TestDialogClientDefaultContact(t *testing.T) {
	client := testClient(t, func(req *sip.Request) *sip.Response {
		return sip.NewResponseFromRequest(req, 200, "OK", nil)
	})

	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "uas", Host: "uas.com"})
	invite.AppendHeader(sip.NewHeader("Contact", "<sip:uac@uac.com>"))
	require.NoError(t, clientRequestBuildReq(client, invite))

	res := sip.NewResponseFromRequest(invite, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Contact", "<sip:uas@uas.com>"))

	s := DialogClientSession{
		UA: &DialogUA{
			Client:     client,
			ContactHDR: sip.ContactHeader{Address: sip.Uri{User: "uac", Host: "uac.com"}},
		},
		Dialog: Dialog{
			InviteRequest:  invite,
			InviteResponse: res,
		},
		inviteTx: sip.NewClientTx("test", invite, nil, slog.Default()),
	}

	// A canceled context builds the request but sends nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range dialogContactCases {
		t.Run(string(tc.method), func(t *testing.T) {
			req := sip.NewRequest(tc.method, res.Contact().Address)
			s.Do(ctx, req)

			assert.Equal(t, tc.contact, req.Contact() != nil)
		})
	}

	t.Run("CallerContactKept", func(t *testing.T) {
		req := sip.NewRequest(sip.INFO, res.Contact().Address)
		req.AppendHeader(sip.NewHeader("Contact", "<sip:own@uac.com>"))
		s.Do(ctx, req)

		require.NotNil(t, req.Contact())
		assert.Equal(t, "own", req.Contact().Address.User)
	})
}

func TestDialogServerDefaultContact(t *testing.T) {
	ua, _ := NewUA()
	defer ua.Close()
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "uas", Host: "127.0.0.200", Port: 5099},
	}
	dialogSrv := NewDialogServerCache(cli, uasContact)

	invite, _, _ := createTestInvite(t, "sip:uas@uas.com", "udp", "uas.com:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uac", Port: 1234}})

	dialog, err := dialogSrv.ReadInvite(invite, sip.NewServerTx("test", invite, nil, slog.Default()))
	require.NoError(t, err)

	// A canceled context builds the request but sends nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range dialogContactCases {
		t.Run(string(tc.method), func(t *testing.T) {
			req := sip.NewRequest(tc.method, invite.Contact().Address)
			dialog.TransactionRequest(ctx, req)

			assert.Equal(t, tc.contact, req.Contact() != nil)
		})
	}
}
