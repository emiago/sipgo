package sipgo

import (
	"strings"
	"testing"

	"github.com/emiago/sipgo/sip"
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

	// Proxy receives this request
	req := createSimpleRequest(sip.INVITE, sender, recipment, "UDP")
	oldvia, _ := req.Via()
	assert.Equal(t, "Via: SIP/2.0/UDP 10.1.1.1:5060;branch="+oldvia.Params["branch"], oldvia.String())

	// Proxy will add via header with client host
	err = ClientRequestAddVia(c, req)
	require.Nil(t, err)
	via, _ := req.Via()
	tmpvia := *via // Save this for later usage
	assert.Equal(t, "Via: SIP/2.0/UDP 10.0.0.0;branch="+via.Params["branch"], via.String())
	assert.NotEqual(t, via.Params["branch"], oldvia.Params["branch"])

	// Add Record Route
	err = ClientRequestAddRecordRoute(c, req)
	require.Nil(t, err)
	rr, _ := req.RecordRoute()

	if strings.Contains(";lr;transport=udp", rr.String()) {
		assert.Equal(t, "Record-Route: <sip:10.0.0.0;lr;transport=udp>", rr.String())
	}
	if strings.Contains(";transport=udp;lr", rr.String()) {
		assert.Equal(t, "Record-Route: <sip:10.0.0.0;transport=udp;lr>", rr.String())
	}

	// When proxy gets response, he will remove via
	res := sip.NewResponseFromRequest(req, 400, "", nil)
	ClientResponseRemoveVia(c, res)
	viaprev, _ := res.Via()
	assert.Equal(t, oldvia, viaprev)

	// Lets make via multivalue
	req = createSimpleRequest(sip.INVITE, sender, recipment, "UDP")
	via, _ = req.Via()
	via.Next = &tmpvia
	res = sip.NewResponseFromRequest(req, 400, "", nil)
	ClientResponseRemoveVia(c, res)
	viaprev, _ = res.Via()
	assert.Equal(t, via.Host, viaprev.Host)
	assert.Nil(t, viaprev.Next)
	// assert.Equal(t, oldvia.Next, nil)
}
