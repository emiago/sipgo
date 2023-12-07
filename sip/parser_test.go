package sip

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

	var uri Uri
	var err error
	var str string

	testCases := []string{
		"sip:alice@atlanta.com",
		"SIP:alice@atlanta.com",
		"sIp:alice@atlanta.com",
	}
	for _, testCase := range testCases {
		err = ParseUri(testCase, &uri)
		require.Nil(t, err)
		assert.Equal(t, "alice", uri.User)
		assert.Equal(t, "atlanta.com", uri.Host)
		assert.False(t, uri.Encrypted)
	}

	testCases = []string{
		"sips:alice@atlanta.com",
		"SIPS:alice@atlanta.com",
		"sIpS:alice@atlanta.com",
	}
	for _, testCase := range testCases {
		err = ParseUri(testCase, &uri)
		require.Nil(t, err)
		assert.Equal(t, "alice", uri.User)
		assert.Equal(t, "atlanta.com", uri.Host)
		assert.True(t, uri.Encrypted)
	}

	uri = Uri{}
	str = "sips:alice@atlanta.com?subject=project%20x&priority=urgent"
	err = ParseUri(str, &uri)
	require.Nil(t, err)

	assert.Equal(t, "alice", uri.User)
	assert.Equal(t, "atlanta.com", uri.Host)
	subject, _ := uri.Headers.Get("subject")
	priority, _ := uri.Headers.Get("priority")
	assert.Equal(t, "project%20x", subject)
	assert.Equal(t, "urgent", priority)

	uri = Uri{}
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

	uri = Uri{}
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
	params := HeaderParams{}
	UnmarshalParams(s, ';', '?', params)
	assert.Equal(t, 2, len(params))
	assert.Equal(t, "tls", params["transport"])
	assert.Equal(t, "", params["lr"])
}

func testParseHeader(t *testing.T, parser *Parser, header string) Header {
	// This is fake way to get parsing done. We use fake message and read first header
	_, h := testParseHeaderOnRequest(t, parser, header)
	return h
}

func testParseHeaderOnRequest(t *testing.T, parser *Parser, header string) (*Request, Header) {
	// This is fake way to get parsing done. We use fake message and read first header
	msg := NewRequest(INVITE, nil)
	name := strings.Split(header, ":")[0]
	err := parser.headersParsers.parseMsgHeader(msg, header)
	require.Nil(t, err)
	return msg, msg.GetHeader(name)
}

func TestParseHeaders(t *testing.T) {
	parser := NewParser()
	t.Run("ViaHeader", func(t *testing.T) {
		branch := GenerateBranch()
		header := "Via: SIP/2.0/UDP 127.0.0.2:5060;rport;branch=" + branch
		h := testParseHeader(t, parser, header)

		hstr := h.String()
		// TODO find better way to compare
		unordered := header[:strings.Index(header, ";")] + ";branch=" + branch + ";rport"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("ToHeader", func(t *testing.T) {
		header := "To: \"Bob\" <sip:bob@127.0.0.1:5060>;xxx=xxx;yyyy=yyyy"
		h := testParseHeader(t, parser, header)

		hstr := h.String()
		unordered := header[:strings.Index(header, ";")] + ";yyyy=yyyy;xxx=xxx"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("FromHeader", func(t *testing.T) {
		header := "From: \"Bob\" <sip:bob@127.0.0.1:5060>"
		h := testParseHeader(t, parser, header)

		hstr := h.String()
		assert.True(t, hstr == header, hstr)
	})

	t.Run("ContactHeader", func(t *testing.T) {

		for header, expected := range map[string]string{
			"Contact: sip:sipp@127.0.0.3:5060":            "Contact: <sip:sipp@127.0.0.3:5060>",
			"Contact: SIPP <sip:sipp@127.0.0.3:5060>":     "Contact: \"SIPP\" <sip:sipp@127.0.0.3:5060>",
			"Contact: <sip:127.0.0.2:5060;transport=UDP>": "Contact: <sip:127.0.0.2:5060;transport=UDP>",
		} {
			req, h := testParseHeaderOnRequest(t, parser, header)

			hstr := h.String()
			assert.Equal(t, expected, hstr)

			// Try fast reference
			hdr, _ := req.Contact()
			assert.IsType(t, &ContactHeader{}, hdr)
		}

		type contactFields struct {
			displayName string
			address     string
			headers     HeaderParams
		}

		for header, expected := range map[string]contactFields{
			"Contact: <sip:2000@dkanrjsk.invalid>;+sip.ice;reg-id=1;+sip.instance=\"<urn:uuid:a369bd8d-f310-4a95-8328-98c7ed3d5439>\";expires=300": {
				address: "sip:2000@dkanrjsk.invalid", headers: map[string]string{"+sip.ice": "", "reg-id": "1", "+sip.instance": "\"<urn:uuid:a369bd8d-f310-4a95-8328-98c7ed3d5439>\"", "expires": "300"}},
			// "m: <sip:test@10.5.0.1:50267;transport=TCP;ob>;reg-id=1;+instance=\"<urn:uuid:00000000-0000-0000-0000-0000eb83488d>\"": {
			// 	address: "sip:test@10.5.0.1:50267;transport=TCP;ob", headers: map[string]string{"reg-id": "1", "+instance": "\"<urn:uuid:00000000-0000-0000-0000-0000eb83488d>\""}},
		} {
			req, _ := testParseHeaderOnRequest(t, parser, header)
			h, _ := req.Contact()

			assert.Equal(t, expected.displayName, h.DisplayName)
			assert.Equal(t, expected.address, h.Address.String())
			assert.Equal(t, expected.headers, h.Params)
		}
	})

	t.Run("RouteHeader", func(t *testing.T) {
		header := "Route: <sip:rr$n=net_me_tls@62.109.228.74:5061;transport=tls;lr>"
		h := testParseHeader(t, parser, header)

		hstr := h.String()
		unordered := header[:strings.Index(header, ";")] + ";lr;transport=tls>"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("RecordRouteHeader", func(t *testing.T) {
		header := "Record-Route: <sip:rr$n=net_me_tls@62.109.228.74:5061;transport=tls;lr>"
		h := testParseHeader(t, parser, header)

		hstr := h.String()
		unordered := header[:strings.Index(header, ";")] + ";lr;transport=tls>"
		assert.True(t, hstr == header || hstr == unordered, hstr)
	})

	t.Run("MaxForwards", func(t *testing.T) {
		header := "Max-Forwards: 70"
		h := testParseHeader(t, parser, header)

		exp := MaxForwardsHeader(70)
		assert.IsType(t, &exp, h)
		assert.Equal(t, "70", h.Value())
		assert.Equal(t, header, h.String())
	})
}

func BenchmarkParserHeaders(b *testing.B) {
	b.Run("ViaHeader", func(b *testing.B) {
		branch := GenerateBranch()
		header := "Via: SIP/2.0/UDP 127.0.0.2:5060;branch=" + branch
		colonIdx := strings.Index(header, ":")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := headerParserVia(header[:colonIdx], header[colonIdx+2:])
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
			_, err := headerParserTo(header[:colonIdx], header[colonIdx+2:])
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
			_, err := headerParserFrom(header[:colonIdx], header[colonIdx+2:])
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
			_, err := headerParserContact(header[:colonIdx], header[colonIdx+2:])
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
			_, err := headerParserCSeq(header[:colonIdx], header[colonIdx+2:])
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
			_, err := headerParserRoute(header[:colonIdx], header[colonIdx+2:])
			if err != nil {
				b.Fatal(err)
			}
		}
	})

}

func TestParseBadMessages(t *testing.T) {
	parser := NewParser()

	// 		The start-line, each message-header line, and the empty line MUST be
	//    terminated by a carriage-return line-feed sequence (CRLF).  Note that
	//    the empty line MUST be present even if the message-body is not.

	t.Run("no empty line between header and body", func(t *testing.T) {
		rawMsg := []string{
			"SIP/2.0 180 Ringing",
			"Via: SIP/2.0/UDP 127.0.0.20:5060;branch=z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22;alias, SIP/2.0/UDP 127.0.0.10:5060;branch=z9hG4bK-543537-1-0",
			"Content-Length: 0",
			"v=0",
		}
		msgstr := strings.Join(rawMsg, "\r\n")
		_, err := parser.ParseSIP([]byte(msgstr))
		require.ErrorIs(t, err, ErrParseInvalidMessage)
	})
	t.Run("finish empty line", func(t *testing.T) {
		rawMsg := []string{
			"SIP/2.0 180 Ringing",
			"Via: SIP/2.0/UDP 127.0.0.20:5060;branch=z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22;alias, SIP/2.0/UDP 127.0.0.10:5060;branch=z9hG4bK-543537-1-0",
			"Content-Length: 0",
			"",
		}
		msgstr := strings.Join(rawMsg, "\r\n")
		_, err := parser.ParseSIP([]byte(msgstr))
		require.Error(t, err, ErrParseInvalidMessage)
	})

}

func TestParseRequest(t *testing.T) {
	branch := GenerateBranch()
	callid := fmt.Sprintf("gotest-%d", time.Now().UnixNano())
	parser := NewParser()
	t.Run("NoCRLF", func(t *testing.T) {
		// https://www.rfc-editor.org/rfc/rfc3261.html#section-7
		// In case of missing CRLF
		m := "INVITE sip:10.5.0.10:5060;transport=udp SIP/2.0\nContent-Length: 0"
		_, err := parser.ParseSIP([]byte(m))
		assert.ErrorIs(t, err, io.EOF)

		for _, msgstr := range []string{
			// "INVITE sip:10.5.0.10:5060;transport=udp SIP/2.0\nContent-Length: 0",
			"INVITE sip:10.5.0.10:5060;transport=udp SIP/2.0\r\nContent-Length: 0\n",
			"INVITE sip:10.5.0.10:5060;transport=udp SIP/2.0\r\nContent-Length: 0\r\n\n",
			"INVITE sip:10.5.0.10:5060;transport=udp SIP/2.0\r\nContent-Length: 10\r\nabcd\nefgh",
		} {
			_, err := parser.ParseSIP([]byte(msgstr))
			assert.ErrorIs(t, err, ErrParseInvalidMessage)
		}
	})

	// Full message
	rawMsg := []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.2:5060;branch=" + branch,
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Contact: <sip:alice@127.0.0.2:5060;expires=3600>",
		"Content-Type: application/sdp",
		"Content-Length: 562",
		"",
		"v=0",
		"o=- 3884881090 3884881090 IN IP4 192.168.178.22",
		"s=pjmedia",
		"b=AS:84",
		"t=0 0",
		"a=X-nat:0",
		"m=audio 58804 RTP/AVP 96 97 98 99 3 0 8 9 120 121 122",
		"c=IN IP4 192.168.178.22",
		"b=TIAS:64000",
		"a=sendrecv",
		"a=rtpmap:96 speex/16000",
		"a=rtpmap:97 speex/8000",
		"a=rtpmap:98 speex/32000",
		"a=rtpmap:99 iLBC/8000",
		"a=fmtp:99 mode=30",
		"a=rtpmap:120 telephone-event/16000",
		"a=fmtp:120 0-16",
		"a=rtpmap:121 telephone-event/8000",
		"a=fmtp:121 0-16",
		"a=rtpmap:122 telephone-event/32000",
		"a=fmtp:122 0-16",
		"a=ssrc:1129373754 cname:2ab364994c4b0b3f",
		"a=rtcp:58805 IN IP4 192.168.178.22",
		"a=rtcp-mux",
		"",
	}

	msgstr := strings.Join(rawMsg, "\r\n")

	msg, err := parser.ParseSIP([]byte(msgstr))
	require.Nil(t, err)

	from, exists := msg.From()
	require.True(t, exists)
	to, exists := msg.To()
	require.True(t, exists)

	contact := msg.GetHeaders("Contact")
	require.NotNil(t, contact)

	assert.Equal(t, "127.0.0.2:5060", from.Address.Host+":"+strconv.Itoa(from.Address.Port))

	assert.Equal(t, to.Address.Host+":"+strconv.Itoa(to.Address.Port), "127.0.0.1:5060")
	assert.Equal(t, to.Address.Host+":"+strconv.Itoa(to.Address.Port), "127.0.0.1:5060")

	assert.Equal(t, msg.String(), msgstr)
}

func TestParseResponse(t *testing.T) {
	rawMsg := []string{
		"SIP/2.0 180 Ringing",
		"Via: SIP/2.0/UDP 127.0.0.20:5060;branch=z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22;alias, SIP/2.0/UDP 127.0.0.10:5060;branch=z9hG4bK-543537-1-0",
		"From: \"sipp\" <sip:sipp@127.0.0.10:5060>;tag=543537SIPpTag001",
		"To: \"service\" <sip:service@127.0.0.20:5060>;tag=543447SIPpTag011",
		"Call-ID: 1-543537@127.0.0.10",
		"CSeq: 1 INVITE",
		"Contact: <sip:127.0.0.30:5060;transport=UDP>",
		"Content-Length: 0",
		"",
		"",
	}

	data := []byte(strings.Join(rawMsg, "\r\n"))

	parser := NewParser()
	msg, err := parser.ParseSIP(data)
	require.Nil(t, err, err)
	r := msg.(*Response)

	// Check all headers exists, but do not check is parsing ok. We do this in different tests
	// Use some value to make sure header is there

	// Make sure via ref is correct set
	via, _ := r.Via()
	assert.Equal(t, "z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22", via.Params["branch"])

	// Check all vias branch
	vias := r.GetHeaders("via")
	assert.Equal(t, "z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22", vias[0].(*ViaHeader).Params["branch"])
	assert.Equal(t, "z9hG4bK-543537-1-0", vias[1].(*ViaHeader).Params["branch"])
	// Check no comma present
	assert.False(t, strings.Contains(vias[1].String(), ","))

	from, _ := r.From()
	assert.Equal(t, "sipp", from.Address.User)

	to, _ := r.To()
	assert.Equal(t, "service", to.Address.User)

	c, _ := r.Contact()
	assert.Equal(t, "", c.Address.User)
}

func TestRegisterRequestFail(t *testing.T) {
	rawMsg := []string{
		"REGISTER sip:10.5.0.10:5060;transport=udp SIP/2.0",
		"v: SIP/2.0/UDP 10.5.0.1:51477;rport;branch=z9hG4bKPj55659194-de09-497e-8cd0-978755d148bc",
		"Route: <sip:10.5.0.10:5060;transport=udp;lr>",
		"Route: <sip:10.5.0.10:5060;transport=udp;lr>",
		"Max-Forwards: 70",
		"f: <sip:test@10.5.0.10>;tag=171a9361-dd7b-49a8-831b-16691c419860",
		"t: <sip:test@10.5.0.10>",
		"i: 6d3e7e31-f58e-4d7e-8bc3-1c7efa230424",
		"CSeq: 10330 REGISTER",
		"User-Agent: PJSUA v2.10 Linux-5.14.4.18/x86_64/glibc-2.31",
		"m: <sip:test@10.5.0.1:51477;ob>",
		"Expires: 30",
		"Allow: PRACK, INVITE, ACK, BYE, CANCEL, UPDATE, INFO, SUBSCRIBE, NOTIFY, REFER, MESSAGE, OPTIONS",
		"l:  0",
		"",
		"",
	}

	data := []byte(strings.Join(rawMsg, "\r\n"))

	parser := NewParser()
	msg, err := parser.ParseSIP(data)
	require.Nil(t, err, err)
	req := msg.(*Request)

	c, exists := req.Contact()
	require.True(t, exists)
	assert.Equal(t, "test", c.Address.User)
}

// https://www.rfc-editor.org/rfc/rfc4475#section-3.1.1
func TestSIPTortuous(t *testing.T) {
	// This currently parses without error but fails parsing on header level
	if os.Getenv("TORTUOUS_TEST") == "" {
		t.Skip()
	}
	rawMsg := []string{
		`INVITE sip:vivekg@chair-dnrc.example.com;unknownparam SIP/2.0`,
		`TO :
sip:vivekg@chair-dnrc.example.com ;   tag    = 1918181833n`,
		`from   : "J Rosenberg \\\""       <sip:jdrosen@example.com>
;
tag = 98asjd8`,
		`MaX-fOrWaRdS: 0068`,
		`Call-ID: wsinv.ndaksdj@192.0.2.1`,
		`Content-Length   : 150`,
		`cseq: 0009
  INVITE`,
		`Via  : SIP  /   2.0
/UDP
	192.0.2.2;branch=390skdjuw`,
		`s :`,
		`NewFangledHeader:   newfangled value
 continued newfangled value`,
		`UnknownHeaderWithUnusualValue: ;;,,;;,;`,
		`Content-Type: application/sdp`,
		`Route:
<sip:services.example.com;lr;unknownwith=value;unknown-no-value>`,
		`v:  SIP  / 2.0  / TCP     spindle.example.com   ;
branch  =   z9hG4bK9ikj8  ,
SIP  /    2.0   / UDP  192.168.255.111   ; branch=
z9hG4bK30239`,
		`m:"Quoted string \"\"" <sip:jdrosen@example.com> ; newparam =
	newvalue ;
secondparam ; q = 0.33`,
		``,
		`v=0
o=mhandley 29739 7272939 IN IP4 192.0.2.3
s=-
c=IN IP4 192.0.2.4
t=0 0
m=audio 49217 RTP/AVP 0 12
m=video 3227 RTP/AVP 31
a=rtpmap:31 LPC`,
	}

	data := []byte(strings.Join(rawMsg, "\r\n"))
	parser := NewParser()
	msg, err := parser.ParseSIP(data)
	require.Nil(t, err, err)

	// TODO check each header
	t.Log(msg.String())
}

func BenchmarkParser(b *testing.B) {
	branch := GenerateBranch()
	callid := fmt.Sprintf("gotest-%d", time.Now().UnixNano())
	rawMsg := []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.2:5060;branch=" + branch,
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Contact: <sip:alice@127.0.0.2:5060>",
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
		"",
	}
	data := []byte(strings.Join(rawMsg, "\r\n"))
	parser := NewParser()
	b.ResetTimer()

	b.Run("SingleRoutine", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			msg, err := parser.ParseSIP(data)
			if err != nil {
				b.Fatal(err)
			}
			if req, _ := msg.(*Request); !req.IsInvite() {
				b.Fatal("Not INVITE")
			}
		}
	})

	b.Run("Paralel", func(b *testing.B) {
		b.RunParallel(func(p *testing.PB) {
			i := 0
			for p.Next() {
				msg, err := parser.ParseSIP(data)
				if err != nil {
					b.Fatal(err)
				}
				if req, _ := msg.(*Request); !req.IsInvite() {
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
		_, err := parseLine(d)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParserAddressValue(b *testing.B) {
	header := "To: \"Bob\" <sip:bob:pass@127.0.0.1:5060>;tag=1928301774;xxx=xxx;yyyy=yyyy"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := headerParserTo("To", header[4:])
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParserNoData(b *testing.B) {
	output := make(chan Message)
	// errs := make(chan error)
	branch := GenerateBranch()
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
			parser.ParseSIP(data)
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
