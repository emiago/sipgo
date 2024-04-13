package sip

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCreateMessage(t testing.TB, rawMsg []string) Message {
	msg, err := ParseMessage([]byte(strings.Join(rawMsg, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func testCreateInvite(t testing.TB, targetSipUri string, transport, fromAddr string) (r *Request, callid string, ftag string) {
	branch := GenerateBranch()
	callid = "gotest-" + time.Now().Format(time.RFC3339Nano)
	ftag = fmt.Sprintf("%d", time.Now().UnixNano())
	return testCreateMessage(t, []string{
		"INVITE " + targetSipUri + " SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + fromAddr + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + fromAddr + ">;tag=" + ftag,
		"To: \"Bob\" <" + targetSipUri + ">",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	}).(*Request), callid, ftag
}

func TestResolveInterfaceIP(t *testing.T) {
	ip, iface, err := ResolveInterfacesIP("ip4", nil)
	require.NoError(t, err)
	require.NotNil(t, ip)

	t.Log(ip.String(), len(ip), iface.Name)
	assert.False(t, ip.IsLoopback())
	assert.NotNil(t, ip.To4())

	ip, iface, err = ResolveInterfacesIP("ip6", nil)
	require.NoError(t, err)
	require.NotNil(t, ip)

	t.Log(ip.String(), len(ip), iface.Name)
	assert.False(t, ip.IsLoopback())
	assert.NotNil(t, ip.To16())
}

func BenchmarkHeaderToLower(b *testing.B) {
	//BenchmarkHeaderToLower-8   	1000000000	         1.033 ns/op	       0 B/op	       0 allocs/op
	h := "Content-Type"
	for i := 0; i < b.N; i++ {
		s := HeaderToLower(h)
		if s != "content-type" {
			b.Fatal("Header not lowered")
		}
	}
}
