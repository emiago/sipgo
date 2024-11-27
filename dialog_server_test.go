package sipgo

import (
	"context"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialogServerByeRequest(t *testing.T) {
	ua, _ := NewUA()
	defer ua.Close()
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}
	dialogSrv := NewDialogServerCache(cli, uasContact)

	invite, _, _ := createTestInvite(t, "sip:uas@uas.com", "udp", "uas.com:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uas", Port: 1234}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P1", Port: 5060}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P2", Port: 5060}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P3", Port: 5060}})

	dialog, err := dialogSrv.ReadInvite(invite, sip.NewServerTx("test", invite, nil, log.Logger))
	require.NoError(t, err)

	res := sip.NewResponseFromRequest(invite, sip.StatusOK, "OK", nil)
	res.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uac", Port: 9876}})

	bye := sip.NewRequest(sip.BYE, invite.Contact().Address)
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	// No execution
	dialog.TransactionRequest(ctxCanceled, bye)
	require.Equal(t, invite.CallID(), bye.CallID())

	routes := bye.GetHeaders("Route")
	assert.Equal(t, "<sip:P3:5060>", routes[0].Value())
	assert.Equal(t, "<sip:P2:5060>", routes[1].Value())
	assert.Equal(t, "<sip:P1:5060>", routes[2].Value())
}
