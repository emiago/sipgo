package sipgo

import (
	"testing"

	"github.com/emiraganov/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRequestOptions(t *testing.T) {
	ua, err := NewUA(WithIP("10.0.0.0:5060"))
	require.Nil(t, err)

	c, err := NewClient(ua)
	require.Nil(t, err)

	sender := sip.Uri{
		User: "alice",
		Host: "10.1.1.1",
		Port: 5060,
	}

	recipment := sip.Uri{
		User: "bob",
		Host: "10.2.2.2",
		Port: 5060,
	}

	req := createSimpleRequest(sip.INVITE, sender, recipment, "UDP")
	oldvia, _ := req.Via()

	// Add  via
	err = ClientRequestAddVia(c, req)
	require.Nil(t, err)
	via, _ := req.Via()
	assert.Equal(t, "Via: SIP/2.0/UDP 10.0.0.0;branch="+oldvia.Params["branch"], via.String())

	// Add Record Route
	err = ClientRequestAddRecordRoute(c, req)
	require.Nil(t, err)
	rr, _ := req.RecordRoute()
	assert.Equal(t, "Record-Route: <sip:10.0.0.0;lr;transport=udp>", rr.String())

	// Fake response
	res := sip.NewResponseFromRequest(req, 400, "", nil)
	ClientResponseRemoveVia(c, res)
	viaprev, _ := res.Via()
	assert.Equal(t, oldvia, viaprev)
}
