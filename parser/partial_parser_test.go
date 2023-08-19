package parser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var CR = []byte{'\r'}
var LF = []byte{'\n'}

func TestIsContentLengthHeader(t *testing.T) {
	assert.False(t, isContentLengthHeader(bytes.NewBufferString("").Bytes()))
	assert.False(t, isContentLengthHeader(bytes.NewBufferString("a").Bytes()))

	assert.False(t, isContentLengthHeader(bytes.NewBufferString("content-length").Bytes()))
	assert.True(t, isContentLengthHeader(bytes.NewBufferString("content-length:").Bytes()))
	assert.True(t, isContentLengthHeader(bytes.NewBufferString("cOnTeNt-LeNgTh:").Bytes()))
}

func TestParserSkipsWhitespace(t *testing.T) {
	t.Skip()
	topLineFragment := bytes.NewBufferString("\r\n\n\rI")

	parser := PartialMessageParser{}
	msgBytes := topLineFragment.Bytes()

	for _, b := range msgBytes[:len(msgBytes)-1] {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Start), parser.state)
	}

	messages, err := parser.Process([]byte{msgBytes[len(msgBytes)-1]})
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(TopLine), parser.state)
}

func TestParseTopLineUntilCRLF(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0")

	parser := PartialMessageParser{}
	msgBytes := topLine.Bytes()

	for _, b := range msgBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(TopLine), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(TopLine), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)
}

func TestParseOneHeaderAndThenALineWithOnlyCRLFZeroContentLength(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	singleHeader := bytes.NewBufferString("Content-Length: 0\r\n")

	parser := PartialMessageParser{}
	parser.Process(topLine.Bytes())

	singleHeaderBytes := singleHeader.Bytes()

	for _, b := range singleHeaderBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, err)
	assert.Len(t, messages, 1)
	assert.Equal(t, partialMessageParserState(Start), parser.state)
}

func TestParseOneHeaderAndThenALineWithOnlyCRLFNonZeroContentLength(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	singleHeader := bytes.NewBufferString("Content-Length: 1\r\n")

	parser := PartialMessageParser{}
	parser.Process(topLine.Bytes())

	singleHeaderBytes := singleHeader.Bytes()

	for _, b := range singleHeaderBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Content), parser.state)
}

func TestParseTwoHeadersAndThenALineWithOnlyCRLFZeroContentLength(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	threeHeaders := bytes.NewBufferString("Pizza: 123\r\nPie: \"adsfad\"\r\nContent-Length: 0\r\n")

	parser := PartialMessageParser{}
	parser.Process(topLine.Bytes())

	threeHeaderBytes := threeHeaders.Bytes()

	for _, b := range threeHeaderBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Len(t, messages, 1)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Start), parser.state)
}

func TestParseTwoHeadersAndThenALineWithOnlyCRLFNonZeroContentLength(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	threeHeaders := bytes.NewBufferString("Pizza: 123\r\nPie: \"adsfad\"\r\nContent-Length: 1\r\n")

	parser := PartialMessageParser{}
	parser.Process(topLine.Bytes())

	threeHeaderBytes := threeHeaders.Bytes()

	for _, b := range threeHeaderBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Content), parser.state)
}

func TestParseMessageThatIsMissingContentLengthReturnsError(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	singleHeader := bytes.NewBufferString("Pizza: 0\r\n")

	parser := PartialMessageParser{}
	parser.Process(topLine.Bytes())

	singleHeaderBytes := singleHeader.Bytes()

	for _, b := range singleHeaderBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, messages)
	assert.NotNil(t, err)
	assert.Equal(t, partialMessageParserState(Content), parser.state)
}

func TestParseMessage(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	threeHeaders := bytes.NewBufferString("Pizza: 123\r\nPie: \"adsfad\"\r\nContent-Length: 32\r\n")

	parser := PartialMessageParser{}
	parser.Process(topLine.Bytes())

	singleHeaderBytes := threeHeaders.Bytes()

	for _, b := range singleHeaderBytes {
		_, err := parser.Process([]byte{b})
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Content), parser.state)

	for i := 0; i < 31; i++ {
		messages, err := parser.Process([]byte{byte('a')})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Content), parser.state)
	}

	messages, err = parser.Process([]byte{byte('a')})
	assert.Len(t, messages, 1)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Start), parser.state)

	message := messages[0]
	assert.NotNil(t, message)
	assert.Equal(t, topLine.String()+threeHeaders.String()+"\r\n"+strings.Repeat("a", 32), string(message))
}

func TestParseTwoMessages(t *testing.T) {
	topLine := bytes.NewBufferString("INVITE sip:10.5.0.10:5060;transport=tcp SIP/2.0\r\n")
	threeHeaders := bytes.NewBufferString("Pizza: 123\r\nPie: \"adsfad\"\r\nContent-Length: 32\r\n")

	parser := PartialMessageParser{}

	// Parse one full message first
	parser.Process(topLine.Bytes())
	parser.Process(threeHeaders.Bytes())
	parser.Process(CR)
	parser.Process(LF)
	parser.Process(bytes.Repeat([]byte{'a'}, 32))

	assert.Equal(t, partialMessageParserState(Start), parser.state)

	parser.Process(topLine.Bytes())

	singleHeaderBytes := threeHeaders.Bytes()

	for _, b := range singleHeaderBytes {
		messages, err := parser.Process([]byte{b})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Headers), parser.state)
	}

	messages, err := parser.Process(CR)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Headers), parser.state)

	messages, err = parser.Process(LF)
	assert.Nil(t, messages)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Content), parser.state)

	for i := 0; i < 31; i++ {
		messages, err := parser.Process([]byte{'a'})
		assert.Nil(t, messages)
		assert.Nil(t, err)
		assert.Equal(t, partialMessageParserState(Content), parser.state)
	}

	messages, err = parser.Process([]byte{'a'})
	assert.Len(t, messages, 1)
	assert.Nil(t, err)
	assert.Equal(t, partialMessageParserState(Start), parser.state)

	message := messages[0]
	assert.NotNil(t, message)
	assert.Equal(t, topLine.String()+threeHeaders.String()+"\r\n"+strings.Repeat("a", 32), string(message))
}

func TestParseFullMessage(t *testing.T) {
	parser := PartialMessageParser{}

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
		"",
	}
	msg := bytes.NewBufferString(strings.Join(lines, "\r\n"))

	messages, err := parser.Process(msg.Bytes())

	assert.Len(t, messages, 1)
	assert.Nil(t, err)
}

func TestParseDialogWithInviteAckBye(t *testing.T) {
	parser := PartialMessageParser{}

	lines := []string{
		"INVITE sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:41257;branch=z9hG4bK39b286f199521937c935bd60c90c1456;rport",
		"Call-ID: 7ad13687285b4d1c60cf0f6f745e248e",
		"CSeq: 100 INVITE",
		"Contact: <sip:192.168.1.155:41257;transport=tcp>;some.tag.here;other-tag=here",
		"From: <sip:192.168.1.155>;tag=18721124cc882f28",
		"To: <sip:192.168.1.254:5060>",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Session-Expires: 1800",
		"Session-ID: 57cf2c96188b51a29f98b4c57a26fcf7;remote=00000000000000000000000000000000",
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
		"m=audio 2352 RTP/AVP 114 107 108 104 105 9 18 8 0 101 123",
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
		"m=video 2366 RTP/AVP 99 97 126 123",
		"b=TIAS:6000000",
		"a=rtpmap:99 H265/90000",
		"a=fmtp:99 level-id=90;max-lsr=125337600;max-lps=2088960;max-tr=22;max-tc=20;max-fps=6000;x-extra-tags=123",
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
		"m=application 2449 UDP/BFCP *",
		"a=setup:actpass",
		"a=confid:1",
		"a=userid:19",
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
		"m=application 2408 RTP/AVP 100",
		"a=rtpmap:100 H224/4800",
		"a=sendrecv",
		"m=application 2464 UDP/UDT/IX *",
		"a=ixmap:0 ping",
		"a=ixmap:2 xccp",
		"a=setup:actpass",
		"a=fingerprint:sha-1 0A:58:47:C4:8E:74:30:53:5F:AF:5E:25:CB:44:A9:CF:3B:87:D7:BF",
		"ACK sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:41257;branch=z9hG4bKc02c1e1e78023fde8b20ec9232b0443e;rport",
		"Call-ID: 7ad13687285b4d1c60cf0f6f745e248e",
		"CSeq: 100 ACK",
		"From: <sip:192.168.1.155>;tag=18721124cc882f28",
		"To: <sip:192.168.1.254:5060>;tag=a0ee9e0a-9032-4083-850f-c75c6a5a9aa5",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Content-Length: 0",
		"",
		"BYE sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:41257;branch=z9hG4bKd4c2f1065004520758880ebc497ebef5;rport",
		"Call-ID: 7ad13687285b4d1c60cf0f6f745e248e",
		"CSeq: 116 BYE",
		"Contact: <sip:192.168.1.155:41257;transport=tcp>;some.tag.here;other-tag=here",
		"From: <sip:192.168.1.155>;tag=18721124cc882f28",
		"To: <sip:192.168.1.254:5060>;tag=a0ee9e0a-9032-4083-850f-c75c6a5a9aa5",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v. 2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Content-Length: 0",
		"",
		"",
	}

	msgs := bytes.NewBufferString(strings.Join(lines, "\r\n"))
	messages, err := parser.Process(msgs.Bytes())

	assert.Nil(t, err)
	assert.Equal(t, 3, len(messages))
}

func BenchmarkPartialParser(b *testing.B) {
	lines := []string{
		"INVITE sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:41257;branch=z9hG4bK39b286f199521937c935bd60c90c1456;rport",
		"Call-ID: 7ad13687285b4d1c60cf0f6f745e248e",
		"CSeq: 100 INVITE",
		"Contact: <sip:192.168.1.155:41257;transport=tcp>;some.tag.here;other-tag=here",
		"From: <sip:192.168.1.155>;tag=18721124cc882f28",
		"To: <sip:192.168.1.254:5060>",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Session-Expires: 1800",
		"Session-ID: 57cf2c96188b51a29f98b4c57a26fcf7;remote=00000000000000000000000000000000",
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
		"m=audio 2352 RTP/AVP 114 107 108 104 105 9 18 8 0 101 123",
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
		"m=video 2366 RTP/AVP 99 97 126 123",
		"b=TIAS:6000000",
		"a=rtpmap:99 H265/90000",
		"a=fmtp:99 level-id=90;max-lsr=125337600;max-lps=2088960;max-tr=22;max-tc=20;max-fps=6000;x-extra-tags=123",
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
		"m=application 2449 UDP/BFCP *",
		"a=setup:actpass",
		"a=confid:1",
		"a=userid:19",
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
		"m=application 2408 RTP/AVP 100",
		"a=rtpmap:100 H224/4800",
		"a=sendrecv",
		"m=application 2464 UDP/UDT/IX *",
		"a=ixmap:0 ping",
		"a=ixmap:2 xccp",
		"a=setup:actpass",
		"a=fingerprint:sha-1 0A:58:47:C4:8E:74:30:53:5F:AF:5E:25:CB:44:A9:CF:3B:87:D7:BF",
		"ACK sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:41257;branch=z9hG4bKc02c1e1e78023fde8b20ec9232b0443e;rport",
		"Call-ID: 7ad13687285b4d1c60cf0f6f745e248e",
		"CSeq: 100 ACK",
		"From: <sip:192.168.1.155>;tag=18721124cc882f28",
		"To: <sip:192.168.1.254:5060>;tag=a0ee9e0a-9032-4083-850f-c75c6a5a9aa5",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Content-Length: 0",
		"",
		"BYE sip:192.168.1.254:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 192.168.1.155:41257;branch=z9hG4bKd4c2f1065004520758880ebc497ebef5;rport",
		"Call-ID: 7ad13687285b4d1c60cf0f6f745e248e",
		"CSeq: 116 BYE",
		"Contact: <sip:192.168.1.155:41257;transport=tcp>;some.tag.here;other-tag=here",
		"From: <sip:192.168.1.155>;tag=18721124cc882f28",
		"To: <sip:192.168.1.254:5060>;tag=a0ee9e0a-9032-4083-850f-c75c6a5a9aa5",
		"Max-Forwards: 70",
		"Allow: INVITE,ACK,CANCEL,BYE,UPDATE,INFO,OPTIONS,REFER,NOTIFY",
		"User-Agent: MyUserAgent v. 2.3.6. b53ee2632df (DEV) Client",
		"Supported: replaces,100rel,timer,gruu,path,outbound",
		"Content-Length: 0",
		"",
		"",
	}

	msgs := bytes.NewBufferString(strings.Join(lines, "\r\n"))
	parser := PartialMessageParser{}

	b.ResetTimer()
	b.StartTimer()

	for n := 0; n < b.N; n++ {
		parser.Process(msgs.Bytes())
	}
}
