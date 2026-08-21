package sip

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// benchInvite is one complete INVITE with an SDP body as it would arrive framed on a TCP stream.
var benchInvite = buildBenchInvite()

// benchInviteSplitA/B are benchInvite cut inside the headers, for the partial-message path.
var benchInviteSplitA, benchInviteSplitB = splitBenchInvite()

// benchInvite3 is three complete messages coalesced into a single read.
var benchInvite3 = bytes.Repeat(benchInvite, 3)

var (
	benchMsgSink Message
	benchLenSink int
)

func buildBenchInvite() []byte {
	body := strings.Join([]string{
		"v=0",
		"o=user1 53655765 2353687637 IN IP4 127.0.0.3",
		"s=-",
		"c=IN IP4 127.0.0.3",
		"t=0 0",
		"m=audio 6000 RTP/AVP 0 8 101",
		"a=rtpmap:0 PCMU/8000",
		"a=rtpmap:8 PCMA/8000",
		"a=rtpmap:101 telephone-event/8000",
		"a=sendrecv",
		"",
	}, "\r\n")

	head := strings.Join([]string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/TCP 127.0.0.2:5060;branch=z9hG4bK.aBcDeF0123456789;rport",
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: gotest-1234567890abcdef@127.0.0.2",
		"CSeq: 1 INVITE",
		"Contact: <sip:alice@127.0.0.2:5060;transport=tcp>",
		"Max-Forwards: 70",
		"User-Agent: sipgo-bench/1.0",
		"Content-Type: application/sdp",
		fmt.Sprintf("Content-Length: %d", len(body)),
		"",
		"",
	}, "\r\n")

	return []byte(head + body)
}

func splitBenchInvite() ([]byte, []byte) {
	i := bytes.Index(benchInvite, []byte("Call-ID:"))
	if i <= 0 {
		panic("bench fixture: no Call-ID")
	}
	return benchInvite[:i], benchInvite[i:]
}

func BenchmarkParseSIPStream(b *testing.B) {
	parser := NewParser()

	b.Run("whole", func(b *testing.B) {
		p := parser.NewSIPStream()
		cb := func(msg Message) { benchMsgSink = msg }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.ParseSIPStream(benchInvite, cb); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("split2", func(b *testing.B) {
		p := parser.NewSIPStream()
		cb := func(msg Message) { benchMsgSink = msg }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.ParseSIPStream(benchInviteSplitA, cb); err != ErrParseSipPartial {
				b.Fatal("expected partial, got", err)
			}
			if err := p.ParseSIPStream(benchInviteSplitB, cb); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("coalesced3", func(b *testing.B) {
		p := parser.NewSIPStream()
		cb := func(msg Message) { benchMsgSink = msg }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.ParseSIPStream(benchInvite3, cb); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkParseSIPStreamRaw(b *testing.B) {
	parser := NewParser()

	b.Run("whole", func(b *testing.B) {
		p := parser.NewSIPStream()
		cb := func(msg Message, raw []byte) { benchMsgSink, benchLenSink = msg, len(raw) }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.ParseSIPStreamRaw(benchInvite, cb); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("split2", func(b *testing.B) {
		p := parser.NewSIPStream()
		cb := func(msg Message, raw []byte) { benchMsgSink, benchLenSink = msg, len(raw) }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.ParseSIPStreamRaw(benchInviteSplitA, cb); err != ErrParseSipPartial {
				b.Fatal("expected partial, got", err)
			}
			if err := p.ParseSIPStreamRaw(benchInviteSplitB, cb); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("coalesced3", func(b *testing.B) {
		p := parser.NewSIPStream()
		cb := func(msg Message, raw []byte) { benchMsgSink, benchLenSink = msg, len(raw) }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.ParseSIPStreamRaw(benchInvite3, cb); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type noopMessageTracer struct{}

func (noopMessageTracer) SIPTraceMessageRead(transport string, laddr string, raddr string, sipmsg []byte) {
	benchLenSink = len(sipmsg)
}

func (noopMessageTracer) SIPTraceMessageWrite(transport string, laddr string, raddr string, sipmsg []byte) {
	benchLenSink = len(sipmsg)
}

func BenchmarkTraceMessageRead(b *testing.B) {
	prev := sipmsgtracer
	b.Cleanup(func() { sipmsgtracer = prev })

	b.Run("noTracer", func(b *testing.B) {
		sipmsgtracer = nil
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			traceMessageRead("tcp", "127.0.0.2:5060", "127.0.0.1:5060", benchInvite)
		}
	})

	b.Run("noopTracer", func(b *testing.B) {
		sipmsgtracer = noopMessageTracer{}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			traceMessageRead("tcp", "127.0.0.2:5060", "127.0.0.1:5060", benchInvite)
		}
	})
}
