package sip

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
