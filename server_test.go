package sipgo

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emiraganov/sipgo/parser"
	"github.com/emiraganov/sipgo/sip"
)

func testCreateMessage(t testing.TB, rawMsg []string) sip.Message {
	msg, err := parser.ParseMessage([]byte(strings.Join(rawMsg, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func createTestInvite(t *testing.T, transport, addr string) (*sip.Request, string, string) {
	branch := sip.GenerateBranch()
	callid := "gotest-" + time.Now().Format(time.RFC3339Nano)
	ftag := fmt.Sprintf("%d", time.Now().UnixNano())
	return testCreateMessage(t, []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + addr + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + addr + ">;tag=" + ftag,
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	}).(*sip.Request), callid, ftag
}

func createTestBye(t *testing.T, transport, addr string, callid string, ftag string, totag string) *sip.Request {
	branch := sip.GenerateBranch()
	return testCreateMessage(t, []string{
		"BYE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + addr + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + addr + ">;tag=" + ftag,
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>;tag=" + totag,
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	}).(*sip.Request)
}

func BenchmarkSwitchVsMap(b *testing.B) {

	b.Run("map", func(b *testing.B) {
		m := map[string]RequestHandler{
			"INVITE":    nil,
			"ACK":       nil,
			"CANCEL":    nil,
			"BYE":       nil,
			"REGISTER":  nil,
			"OPTIONS":   nil,
			"SUBSCRIBE": nil,
			"NOTIFY":    nil,
			"REFER":     nil,
			"INFO":      nil,
			"MESSAGE":   nil,
			"PRACK":     nil,
			"UPDATE":    nil,
			"PUBLISH":   nil,
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := m["REGISTER"]; !ok {
				b.FailNow()
			}

			if _, ok := m["PUBLISH"]; !ok {
				b.FailNow()
			}
		}
	})

	b.Run("switch", func(b *testing.B) {
		m := func(s string) (RequestHandler, bool) {
			switch s {
			case "INVITE":
				return nil, true
			case "ACK":
				return nil, true
			case "CANCEL":
				return nil, true
			case "BYE":
				return nil, true
			case "REGISTER":
				return nil, true
			case "OPTIONS":
				return nil, true
			case "SUBSCRIBE":
				return nil, true
			case "NOTIFY":
				return nil, true
			case "REFER":
				return nil, true
			case "INFO":
				return nil, true
			case "MESSAGE":
				return nil, true
			case "PRACK":
				return nil, true
			case "UPDATE":
				return nil, true
			case "PUBLISH":
				return nil, true

			}
			return nil, false
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := m("REGISTER"); !ok {
				b.FailNow()
			}

			if _, ok := m("NOTIFY"); !ok {
				b.FailNow()
			}
		}
	})
}
