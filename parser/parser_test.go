package parser

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUri(t *testing.T) {
	/*
		https://datatracker.ietf.org/doc/html/rfc3261#section-19.1.3
		sip:alice@atlanta.com
		sip:alice:secretword@atlanta.com;transport=tcp
		sips:alice@atlanta.com?subject=project%20x&priority=urgent
		sip:+1-212-555-1212:1234@gateway.com;user=phone
		sips:1212@gateway.com
		sip:alice@192.0.2.4
		sip:atlanta.com;method=REGISTER?to=alice%40atlanta.com
		sip:alice;day=tuesday@atlanta.com
	*/

	str := "sip:alice@atlanta.com"
	var uri sip.Uri
	err := ParseUri(str, &uri)
	require.Nil(t, err)
	assert.Equal(t, "alice", uri.User)
	assert.Equal(t, "atlanta.com", uri.Host)

	uri = sip.Uri{}
	str = "sips:alice@atlanta.com?subject=project%20x&priority=urgent"
	err = ParseUri(str, &uri)
	require.Nil(t, err)

	assert.Equal(t, "alice", uri.User)
	assert.Equal(t, "atlanta.com", uri.Host)
	subject, _ := uri.Headers.Get("subject")
	priority, _ := uri.Headers.Get("priority")
	assert.Equal(t, "project%20x", subject)
	assert.Equal(t, "urgent", priority)

	uri = sip.Uri{}
	str = "sip:bob:secret@atlanta.com:9999;rport;transport=tcp;method=REGISTER?to=sip:bob%40biloxi.com"
	err = ParseUri(str, &uri)
	require.Nil(t, err)

	assert.Equal(t, "bob", uri.User)
	assert.Equal(t, "secret", uri.Password)
	assert.Equal(t, "atlanta.com", uri.Host)
	assert.Equal(t, 9999, uri.Port)

	assert.Equal(t, 3, uri.UriParams.Length())
	transport, _ := uri.UriParams.Get("transport")
	method, _ := uri.UriParams.Get("method")
	assert.Equal(t, "tcp", transport)
	assert.Equal(t, "REGISTER", method)

	assert.Equal(t, 1, uri.Headers.Length())
	to, _ := uri.Headers.Get("to")
	assert.Equal(t, "sip:bob%40biloxi.com", to)

	uri = sip.Uri{}
	str = "127.0.0.2:5060;rport;branch=z9hG4bKPj6c65c5d9-b6d0-4a30-9383-1f9b42f97de9"
	err = ParseUri(str, &uri)
	require.Nil(t, err)

	rport, _ := uri.UriParams.Get("rport")
	branch, _ := uri.UriParams.Get("branch")
	assert.Equal(t, "", rport)
	assert.Equal(t, "z9hG4bKPj6c65c5d9-b6d0-4a30-9383-1f9b42f97de9", branch)
}

func TestUnmarshalParams(t *testing.T) {
	s := "transport=tls;lr"
	params := sip.HeaderParams{}
	UnmarshalParams(s, ';', '?', params)
	assert.Equal(t, 2, len(params))
	assert.Equal(t, "tls", params["transport"])
	assert.Equal(t, "", params["lr"])
}

func TestParseHeaders(t *testing.T) {
	parser := NewParser()
	t.Run("ViaHeader", func(t *testing.T) {
		branch := sip.GenerateBranch()
		header := "Via: SIP/2.0/UDP 127.0.0.2:5060;rport;branch=" + branch
		h, err := parser.ParseHeader(header)
		require.Nil(t, err)

		hstr := h.String()
		// TODO find better way to compare
		unordered := header[:strings.Index(header, ";")] + ";branch=" + branch + ";rport"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("ToHeader", func(t *testing.T) {
		header := "To: \"Bob\" <sip:bob@127.0.0.1:5060>;xxx=xxx;yyyy=yyyy"
		h, err := parser.ParseHeader(header)
		require.Nil(t, err)

		hstr := h.String()
		unordered := header[:strings.Index(header, ";")] + ";yyyy=yyyy;xxx=xxx"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("FromHeader", func(t *testing.T) {
		header := "From: \"Bob\" <sip:bob@127.0.0.1:5060>"
		h, err := parser.ParseHeader(header)
		require.Nil(t, err)

		hstr := h.String()
		assert.True(t, hstr == header, hstr)
	})

	t.Run("ContactHeader", func(t *testing.T) {
		for header, expected := range map[string]string{
			"Contact: sip:sipp@127.0.0.3:5060":            "Contact: <sip:sipp@127.0.0.3:5060>",
			"Contact: SIPP <sip:sipp@127.0.0.3:5060>":     "Contact: \"SIPP\" <sip:sipp@127.0.0.3:5060>",
			"Contact: <sip:127.0.0.2:5060;transport=UDP>": "Contact: <sip:127.0.0.2:5060;transport=UDP>",
			// "m: <sip:test@10.5.0.1:50267;transport=TCP;ob>;reg-id=1;+sip.instance=\"<urn:uuid:00000000-0000-0000-0000-0000eb83488d>\"": "Contact: <sip:test@10.5.0.1:50267;transport=TCP;ob>;reg-id=1;+sip.instance=\"<urn:uuid:00000000-0000-0000-0000-0000eb83488d>\"",
		} {
			h, err := parser.ParseHeader(header)
			require.Nil(t, err)
			assert.IsType(t, &sip.ContactHeader{}, h)

			hstr := h.String()
			assert.Equal(t, expected, hstr)
		}
	})

	t.Run("RouteHeader", func(t *testing.T) {
		header := "Route: <sip:rr$n=net_me_tls@62.109.228.74:5061;transport=tls;lr>"
		h, err := parser.ParseHeader(header)
		require.Nil(t, err, err)

		hstr := h.String()
		unordered := header[:strings.Index(header, ";")] + ";lr;transport=tls>"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("RecordRouteHeader", func(t *testing.T) {
		header := "Record-Route: <sip:rr$n=net_me_tls@62.109.228.74:5061;transport=tls;lr>"
		h, err := parser.ParseHeader(header)
		require.Nil(t, err, err)

		hstr := h.String()
		unordered := header[:strings.Index(header, ";")] + ";lr;transport=tls>"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("MaxForwards", func(t *testing.T) {
		header := "Max-Forwards: 70"
		h, err := parser.ParseHeader(header)
		require.Nil(t, err, err)

		exp := sip.MaxForwardsHeader(70)
		assert.IsType(t, &exp, h)
		assert.Equal(t, "70", h.Value())
		assert.Equal(t, header, h.String())
	})
}

func BenchmarkParserHeaders(b *testing.B) {
	b.Run("ViaHeader", func(b *testing.B) {
		branch := sip.GenerateBranch()
		header := "Via: SIP/2.0/UDP 127.0.0.2:5060;branch=" + branch
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := parseViaHeader(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ToHeader", func(b *testing.B) {
		header := "To: \"Bob\" <sip:bob@127.0.0.1:5060>"
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := parseToAddressHeader(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("FromHeader", func(b *testing.B) {
		header := "From: \"Bob\" <sip:bob@127.0.0.1:5060>"
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := parseFromAddressHeader(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ContactHeader", func(b *testing.B) {
		header := "Contact: <sip:sipp@127.0.0.3:5060>"
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := parseContactAddressHeader(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("CSEQ", func(b *testing.B) {
		header := "CSEQ: 1234 INVITE"
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := parseCSeq(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Route", func(b *testing.B) {
		header := "Route: <sip:rr$n=net_me_tls@62.109.228.74:5061;transport=tls;lr>"
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := parseRouteHeader(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

}

func TestParseRequest(t *testing.T) {
	branch := sip.GenerateBranch()
	callid := fmt.Sprintf("gotest-%d", time.Now().UnixNano())

	rawMsg := []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.2:5060;branch=" + branch,
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Contact: <sip:alice@127.0.0.2:5060;expires=3600>",
		"Content-Length: 0",
		"",
		"",
	}

	msgstr := strings.Join(rawMsg, "\r\n")

	parser := NewParser()
	msg, err := parser.Parse([]byte(msgstr))
	require.Nil(t, err)

	from, exists := msg.From()
	require.True(t, exists)
	to, exists := msg.To()
	require.True(t, exists)

	contact := msg.GetHeader("Contact")
	require.NotNil(t, contact)

	assert.Equal(t, "127.0.0.2:5060", from.Address.Host+":"+strconv.Itoa(from.Address.Port))

	assert.Equal(t, to.Address.Host+":"+strconv.Itoa(to.Address.Port), "127.0.0.1:5060")
	assert.Equal(t, to.Address.Host+":"+strconv.Itoa(to.Address.Port), "127.0.0.1:5060")

	assert.Equal(t, msg.String(), msgstr)
}

func TestRegisterRequestFail(t *testing.T) {
	m := `REGISTER sip:10.5.0.10:5060;transport=udp SIP/2.0
v: SIP/2.0/UDP 10.5.0.1:51477;rport;branch=z9hG4bKPj55659194-de09-497e-8cd0-978755d148bc
Route: <sip:10.5.0.10:5060;transport=udp;lr>
Route: <sip:10.5.0.10:5060;transport=udp;lr>
Max-Forwards: 70
f: <sip:test@10.5.0.10>;tag=171a9361-dd7b-49a8-831b-16691c419860
t: <sip:test@10.5.0.10>
i: 6d3e7e31-f58e-4d7e-8bc3-1c7efa230424
CSeq: 10330 REGISTER
User-Agent: PJSUA v2.10 Linux-5.14.4.18/x86_64/glibc-2.31
m: <sip:test@10.5.0.1:51477;ob>
Expires: 30
Allow: PRACK, INVITE, ACK, BYE, CANCEL, UPDATE, INFO, SUBSCRIBE, NOTIFY, REFER, MESSAGE, OPTIONS
l:  0`
	parser := NewParser()
	msg, err := parser.Parse([]byte(m))
	require.Nil(t, err, err)

	c := msg.GetHeader("Contact").(*sip.ContactHeader)
	assert.Equal(t, "test", c.Address.User)
}

func BenchmarkParser(b *testing.B) {
	branch := sip.GenerateBranch()
	callid := fmt.Sprintf("gotest-%d", time.Now().UnixNano())
	rawMsg := []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.2:5060;branch=" + branch,
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Type: application/sdp",
		"Content-Length: 129",
		"",
		"v=0",
		"o=user1 53655765 2353687637 IN IP4 127.0.0.3",
		"s=-",
		"c=IN IP4 127.0.0.3",
		"t=0 0",
		"m=audio 6000 RTP/AVP 0",
		"a=rtpmap:0 PCMU/8000",
	}
	data := []byte(strings.Join(rawMsg, "\r\n"))
	parser := NewParser()
	b.ResetTimer()
	testcase := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			msg, err := parser.Parse(data)
			if err != nil {
				b.Fatal(err)
			}
			if req, _ := msg.(*sip.Request); !req.IsInvite() {
				b.Fatal("Not INVITE")
			}
		}
	}

	b.Run("SingleRoutine", testcase)
	b.Run("Paralel", func(b *testing.B) {
		b.RunParallel(func(p *testing.PB) {
			i := 0
			for p.Next() {
				msg, err := parser.Parse(data)
				if err != nil {
					b.Fatal(err)
				}
				if req, _ := msg.(*sip.Request); !req.IsInvite() {
					b.Fatal("Not INVITE")
				}

				if i%3 == 0 {
					runtime.GC()
				}
				i++
			}
		})
	})

	// b.Run("Paralel", func(b *testing.B) {
	// 	b.RunParallel(func(p *testing.PB) {
	// 		b.ResetTimer()
	// 		for p.Next() {
	// 			testcase(b)
	// 		}
	// 	})
	// })

}

func BenchmarkParseStartLine(b *testing.B) {
	d := "INVITE sip:bob@127.0.0.1:5060 SIP/2.0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ParseLine(d)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParserAddressValue(b *testing.B) {
	header := "To: \"Bob\" <sip:bob:pass@127.0.0.1:5060>;tag=1928301774;xxx=xxx;yyyy=yyyy"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parseToAddressHeader("To", header[4:])
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParserNoData(b *testing.B) {
	output := make(chan sip.Message)
	// errs := make(chan error)
	branch := sip.GenerateBranch()
	msg := []string{
		"INVITE sip:bob@example.com SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.1:9001;branch=" + branch,
		"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
		"To: \"Bob\" <sip:bob@far-far-away.com>",
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	}

	go func() {
		for range output {
		}
	}()

	data := []byte(strings.Join(msg, "\r\n"))
	b.Run("New", func(b *testing.B) {
		parser := NewParser()
		for i := 0; i < b.N; i++ {
			parser.Parse(data)
		}
	})
}

func BenchmarkUriSipComparison(b *testing.B) {

	compareWithLower := func(s string) bool {
		return strings.ToLower(s)[:3] == "sip"
	}

	compareSwitch := func(s string) bool {
		switch s {
		case "sip", "SIP":
			return true
		}
		return false
	}

	uri := "SIP"
	b.ResetTimer()
	b.Run("WithLower", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if !compareWithLower(uri) {
				b.Fatal("This should not be false")
			}
		}
	})

	b.Run("SwitchCompare", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if !compareSwitch(uri) {
				b.Fatal("This should not be false")
			}
		}
	})
}
