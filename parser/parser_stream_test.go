package parser

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func TestParserStreamBadMessage(t *testing.T) {
	parser := ParserStream{}

	// 		The start-line, each message-header line, and the empty line MUST be
	//    terminated by a carriage-return line-feed sequence (CRLF).  Note that
	//    the empty line MUST be present even if the message-body is not.

	t.Run("no empty line between header and body", func(t *testing.T) {
		rawMsg := []string{
			"SIP/2.0 180 Ringing",
			"Via: SIP/2.0/UDP 127.0.0.20:5060;branch=z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22;alias, SIP/2.0/UDP 127.0.0.10:5060;branch=z9hG4bK-543537-1-0",
			"Content-Length: 0",
			"v=0",
			"s=-", // We need at least 2 line to detect bad message
		}
		msgstr := strings.Join(rawMsg, "\r\n")
		_, err := parser.ParseSIPStream([]byte(msgstr))
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
		_, err := parser.ParseSIPStream([]byte(msgstr))
		require.Error(t, err, ErrParseSipPartial)
	})
}

func TestParserStreamMessage(t *testing.T) {
	p := NewParser()
	parser := p.NewSIPStream()

	lines := []string{
		"INVITE sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:44861;branch=z9hG4bK954690f3012120bc5d064d3f7b5d8a24;rport",
		"Call-ID: 25be1c3be64adb89fa2e86772dd99db1",
		"CSeq: 100 INVITE",
		"Contact: <sip:192.168.1.155:44861;transport=tcp>;some.tag.here;other-tag=here",
		"From: <sip:192.168.1.155>;tag=76fb12e7e2241ed6",
		"To: <sip:192.168.1.254:5060>",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Session-Expires: 1800",
		"Session-ID: e937754d76855249814a9b7f8b3bf556;remote=00000000000000000000000000000000",
		"Content-Type: application/sdp",
		"Content-Length: 3119",
		"",
		"v=0",
		"o=something 8 2 IN IP4 192.168.1.155",
		"s=-",
		"c=IN IP4 192.168.1.155",
		"b=AS:6000",
		"t=0 0",
		"a=some-attrs-here",
		"a=other-tags:v1",
		"m=audio 2332 RTP/AVP 114 107 108 104 105 9 18 8 0 101 123",
		"b=TIAS:128000",
		"a=rtpmap:114 opus/48000/2",
		"a=fmtp:114 maxaveragebitrate=128000;stereo=1",
		"a=rtpmap:107 MP4A-LATM/90000",
		"a=fmtp:107 profile-level-id=25;object=23;bitrate=128000",
		"a=rtpmap:108 MP4A-LATM/90000",
		"a=fmtp:108 profile-level-id=24;object=23;bitrate=64000",
		"a=rtpmap:104 G7221/16000",
		"a=fmtp:104 bitrate=32000",
		"a=rtpmap:105 G7221/16000",
		"a=fmtp:105 bitrate=24000",
		"a=rtpmap:9 G722/8000",
		"a=rtpmap:18 G729/8000",
		"a=fmtp:18 annexb=yes",
		"a=rtpmap:8 PCMA/8000",
		"a=rtpmap:0 PCMU/8000",
		"a=rtpmap:101 telephone-event/8000",
		"a=fmtp:101 0-15",
		"a=rtpmap:123 x-ulpfecuc/8000",
		"a=fmtp:123 multi_ssrc=1;feedback=0;max_esel=1450;m=8;max_n=42;FEC_ORDER=FEC_SRTP;non_seq=1",
		"a=extmap:4 http://some.url.goes.here.com/foobarbazbaq",
		"a=sendrecv",
		"m=video 2364 RTP/AVP 99 97 126 123",
		"b=TIAS:6000000",
		"a=rtpmap:99 H265/90000",
		"a=fmtp:99 level-id=90;max-lsr=125337600;max-lps=2088960;max-tr=22;max-tc=20;max-fps=6000;x-other-tags=123",
		"a=rtpmap:97 H264/90000",
		"a=fmtp:97 packetization-mode=0;profile-level-id=428016;max-br=5000;max-mbps=490000;max-fs=8160;max-dpb=16320;max-smbps=490000;max-fps=6000",
		"a=rtpmap:126 H264/90000",
		"a=fmtp:126 packetization-mode=1;profile-level-id=428016;max-br=5000;max-mbps=490000;max-fs=8160;max-dpb=16320;max-smbps=490000;max-fps=6000",
		"a=rtpmap:123 x-ulpfecuc/8000",
		"a=fmtp:123 multi_ssrc=1;feedback=0;max_esel=1450;m=8;max_n=42;FEC_ORDER=FEC_SRTP;non_seq=1",
		"a=label:11",
		"a=answer:full",
		"a=extmap:4 http://some.url.goes.here.com/foobarbazbaq",
		"a=content:main",
		"a=rtcp-fb:* nack pli",
		"a=rtcp-fb:* ccm fir",
		"a=rtcp-fb:* ccm tmmbr",
		"a=rtcp-fb:* ccm pan",
		"a=sendrecv",
		"a=some-video-attr:97 ltrf=3",
		"a=some-video-attr:126 ltrf=3",
		"m=application 2439 UDP/BFCP *",
		"a=setup:actpass",
		"a=confid:1",
		"a=userid:18",
		"a=bfcpver:2 1",
		"a=floorid:2 mstrm:12",
		"a=floorctrl:c-s",
		"a=connection:new",
		"m=video 2354 RTP/AVP 97 126 96 34 123",
		"b=TIAS:6000000",
		"a=rtpmap:97 H264/90000",
		"a=fmtp:97 packetization-mode=0;profile-level-id=428016;max-br=5000;max-mbps=490000;max-fs=32400;max-dpb=64800;max-smbps=490000;max-fps=6000",
		"a=rtpmap:126 H264/90000",
		"a=fmtp:126 packetization-mode=1;profile-level-id=428016;max-br=5000;max-mbps=490000;max-fs=32400;max-dpb=64800;max-smbps=490000;max-fps=6000",
		"a=rtpmap:96 H263-1998/90000",
		"a=fmtp:96 custom=1280,720,3;custom=1024,768,2;custom=1024,576,2;custom=800,600,2;cif4=2;custom=720,480,2;custom=640,480,2;custom=512,288,2;cif=2;custom=352,240,2;qcif=2;maxbr=30000",
		"a=rtpmap:34 H263/90000",
		"a=fmtp:34 cif4=1;cif=1;qcif=1;maxbr=20000",
		"a=rtpmap:123 x-ulpfecuc/8000",
		"a=fmtp:123 multi_ssrc=1;feedback=0;max_esel=1450;m=8;max_n=42;FEC_ORDER=FEC_SRTP;non_seq=1",
		"a=label:12",
		"a=extmap:4 http://some.url.goes.here.com/foobarbazbaq",
		"a=content:slides",
		"a=rtcp-fb:* nack pli",
		"a=rtcp-fb:* ccm fir",
		"a=rtcp-fb:* ccm tmmbr",
		"a=sendrecv",
		"m=application 2392 RTP/AVP 100",
		"a=rtpmap:100 H224/4800",
		"a=sendrecv",
		"m=application 2455 UDP/UDT/IX *",
		"a=ixmap:0 ping",
		"a=ixmap:2 xccp",
		"a=setup:actpass",
		"a=fingerprint:sha-1 0A:58:47:C4:8E:74:30:53:5F:AF:5E:25:CB:44:A9:CF:3B:87:D7:BF",
		"", // Content length includes last CRLF
	}
	data := []byte((strings.Join(lines, "\r\n")))

	// make partials
	part1 := data[:500]
	part2 := data[500:1000]
	part3 := data[1000:]

	t.Run("first run", func(t *testing.T) {
		t.Logf("Parsing part 1:\n%s", string(part1))
		_, err := parser.ParseSIPStream(part1)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrParseSipPartial)

		t.Logf("Parsing part 2:\n%s", string(part2))
		_, err = parser.ParseSIPStream(part2)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrParseSipPartial)

		t.Logf("Parsing part 3:\n%s", string(part3))
		msgs, err := parser.ParseSIPStream(part3)
		msg := msgs[0]
		require.NoError(t, err)
		require.NotNil(t, msg)
		require.Len(t, msg.Body(), 3119)
		// Check is parser reset it self
		require.Nil(t, parser.reader)
	})

	t.Run("second run", func(t *testing.T) {
		part1 := data[:300]
		part2 := data[300:2000]
		part3 := data[2000:]

		parser.ParseSIPStream(part1)
		parser.ParseSIPStream(part2)
		msg, err := parser.ParseSIPStream(part3)
		require.NoError(t, err)
		require.NotNil(t, msg)
		require.Nil(t, parser.reader)
	})

}

func TestParserStreamChunky(t *testing.T) {
	p := NewParser()
	parser := p.NewSIPStream()

	// Broken first line
	line := []byte("INVITE sip:192.168.1.254:5060 SIP/")
	_, err := parser.ParseSIPStream(line)
	require.ErrorIs(t, err, ErrParseSipPartial)

	// Full first line
	line = []byte("2.0\r\n")
	_, err = parser.ParseSIPStream(line)
	require.ErrorIs(t, err, ErrParseSipPartial)

	// broken header
	lines := []string{
		"Via: SIP/2.0",
	}
	data := []byte((strings.Join(lines, "\r\n")))
	_, err = parser.ParseSIPStream(data)
	require.ErrorIs(t, err, ErrParseSipPartial)

	// TODO add more here chunks

	// rest of sip
	lines = []string{
		"/TCP 192.168.1.155:44861;branch=z9hG4bK954690f3012120bc5d064d3f7b5d8a24;rport",
		"Call-ID: 25be1c3be64adb89fa2e86772dd99db1",
		"CSeq: 100 INVITE",
		"Contact: <sip:192.168.1.155:44861;transport=tcp>;some.tag.here;other-tag=here",
		"From: <sip:192.168.1.155>;tag=76fb12e7e2241ed6",
		"To: <sip:192.168.1.254:5060>",
		"Max-Forwards: 70",
		"Content-Type: application/sdp",
		"Content-Length: 9",
		"",
		"123456789",
	}

	data = []byte((strings.Join(lines, "\r\n")))
	_, err = parser.ParseSIPStream(data)
	require.NoError(t, err)
}

func TestParserStreamMultiple(t *testing.T) {
	p := NewParser()
	parser := p.NewSIPStream()
	lines := []string{
		"SIP/2.0 100 Trying",
		"Via: SIP/2.0/quic 192.168.100.11:56410;branch=z9hG4bK.DRYA6NEOgFJO1t91;alias",
		"From: \"sipgo\" <sip:sipgo@192.168.100.11>;tag=ywgNMIh4OhKwGSFa",
		"To: <sips:123@127.1.1.100>",
		"Call-ID: e3644aeb-f2bb-4499-9620-68b5ffd27017",
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"SIP/2.0 200 OK",
		"Via: SIP/2.0/quic 192.168.100.11:56410;branch=z9hG4bK.DRYA6NEOgFJO1t91;alias",
		"From: \"sipgo\" <sip:sipgo@192.168.100.11>;tag=ywgNMIh4OhKwGSFa",
		"To: <sips:123@127.1.1.100>;tag=7f9b9f9b-319b-48f4-98bf-9922c498fcaf",
		"Call-ID: e3644aeb-f2bb-4499-9620-68b5ffd27017",
		"CSeq: 1 INVITE",
		"Content-Length: 183",
		"Content-Type: application/sdp",
		"",
		"v=0",
		"o=user1 3906001344 3906001344 IN IP4 192.168.100.11",
		"s=Sip Go Media",
		"c=IN IP4 192.168.100.11",
		"t=0 0",
		"m=audio 0 RTP/AVP 0 8",
		"a=sendrecv",
		"a=rtpmap:0 PCMU/8000",
		"a=rtpmap:8 PCMA/8000",
	}

	data := []byte((strings.Join(lines, "\r\n")))

	msgs, err := parser.ParseSIPStream(data)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, msgs[0].(*sip.Response).StartLine(), "SIP/2.0 100 Trying")
	require.Equal(t, msgs[1].(*sip.Response).StartLine(), "SIP/2.0 200 OK")

	t.Run("with chunks", func(t *testing.T) {
		chunks := [][]byte{
			data[:100],
			data[100:200],
			data[200:],
		}

		var msgs []sip.Message
		var err error
		for _, c := range chunks {
			msgs, err = parser.ParseSIPStream(c)
		}
		require.NoError(t, err)
		require.Len(t, msgs, 2)
	})
}

func BenchmarkParserStream(b *testing.B) {
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
		"",
	}
	data := []byte(strings.Join(rawMsg, "\r\n"))
	parser := NewParser()

	minsize := len(data) / 3
	chunks := [][]byte{
		data[:minsize], data[minsize : minsize*2], data[minsize*2:],
	}
	b.ResetTimer()

	b.Run("NoChunks", func(b *testing.B) {
		pstream := parser.NewSIPStream()
		for i := 0; i < b.N; i++ {
			var msg sip.Message
			var err error

			msgs, err := pstream.ParseSIPStream(data)
			if err != nil {
				b.Fatal("Parsing failed", err)
			}

			msg = msgs[0]
			if req, _ := msg.(*sip.Request); !req.IsInvite() {
				b.Fatal("Not INVITE")
			}

		}
	})

	b.Run("SingleRoutine", func(b *testing.B) {
		pstream := parser.NewSIPStream()
		for i := 0; i < b.N; i++ {
			var msgs []sip.Message
			var err error

			for _, data := range chunks {
				msgs, err = pstream.ParseSIPStream(data)
			}

			if err != nil {
				b.Fatal("Parsing failed", err)
			}

			msg := msgs[0]
			if req, _ := msg.(*sip.Request); !req.IsInvite() {
				b.Fatal("Not INVITE")
			}
		}
	})

	b.Run("Paralel", func(b *testing.B) {
		b.RunParallel(func(p *testing.PB) {
			i := 0
			pstream := parser.NewSIPStream()
			for p.Next() {
				var msgs []sip.Message
				var err error

				for _, data := range chunks {
					msgs, err = pstream.ParseSIPStream(data)
				}
				if err != nil {
					b.Fatal("Parsing failed", err)
				}
				msg := msgs[0]

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

}
