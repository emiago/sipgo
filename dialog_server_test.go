package sipgo

import (
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func TestDialogServerBye(t *testing.T) {
	// Fake invite and response
	invite, _, _ := createTestInvite(t, "sip:uas@uas.com", "udp", "uas.com:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uas", Port: 1234}})

	res := sip.NewResponseFromRequest(invite, sip.StatusOK, "OK", nil)
	res.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uac", Port: 9876}})

	bye := newByeRequestUAS(invite, res)

	// Callid is same
	require.Equal(t, invite.CallID(), bye.CallID())

	// To and From is reversed
	require.Equal(t, invite.To().Address, bye.From().Address)
	require.Equal(t, invite.To().DisplayName, bye.From().DisplayName)
	require.Equal(t, invite.From().Address, bye.To().Address)
	require.Equal(t, invite.From().DisplayName, bye.To().DisplayName)

	// Record-Routes are converted to Routes
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P1", Port: 5060}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P2", Port: 5060}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P3", Port: 5060}})

	bye = newByeRequestUAS(invite, res)

	routes := bye.GetHeaders("Route")
	require.Equal(t, "<sip:P3:5060>", routes[0].Value())
	require.Equal(t, "<sip:P2:5060>", routes[1].Value())
	require.Equal(t, "<sip:P1:5060>", routes[2].Value())
}
