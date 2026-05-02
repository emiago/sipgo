package sip

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reproducer for sipgo#251: SIP headers with folded continuation lines fail
// to parse. RFC 3261 §7.3.1 (citing RFC 2822) explicitly allows headers to be
// extended over multiple lines by preceding each extra line with at least one
// SP or HT, and the folding plus surrounding whitespace must be treated as a
// single SP. The trace below is from the AT&T VoWiFi production server (a
// real, currently-deployed P-CSCF) reported by @DentonGentry.
func TestParseFoldedHeaders_AT_T_Repro(t *testing.T) {
	rawMsg := []string{
		"SIP/2.0 401 Unauthorized",
		"Call-ID: 01234567",
		"Via: SIP/2.0/TCP [::1]:5060;received=::1;branch=z9hG4bK.1;rport=48512",
		"To: <sip:30123456789@one.att.net>;tag=abcdef",
		"From: \"30123456789\" <sip:30123456789@one.att.net>;tag=abcdef",
		"CSeq: 1 REGISTER",
		"Date: Tue, 12 Aug 2025 01:20:52 GMT",
		"Server: Alcatel-Lucent-HPSS/3.0.3",
		"WWW-Authenticate: Digest realm=\"one.att.net\",",
		"   nonce=\"abcdefghijklmnopqrstuvwxyz=\",",
		"   opaque=\"ALU:abcdefghijklmnopqrstuvwxyz__\",",
		"   algorithm=AKAv1-MD5,",
		"   qop=\"auth\"",
		"Content-Length: 0",
		"",
		"",
	}

	data := []byte(strings.Join(rawMsg, "\r\n"))

	parser := NewParser()
	msg, err := parser.ParseSIP(data)
	require.NoError(t, err, "RFC 3261 §7.3.1 line folding must be supported")
	r, ok := msg.(*Response)
	require.True(t, ok, "expected *Response, got %T", msg)

	// The folded WWW-Authenticate must round-trip with the continuation lines
	// concatenated into a single logical value (per RFC 3261, fold-whitespace
	// becomes a single SP).
	auth := r.GetHeaders("WWW-Authenticate")
	require.NotEmpty(t, auth, "WWW-Authenticate header must be present after fold-aware parsing")

	authStr := auth[0].Value()
	for _, expect := range []string{
		`realm="one.att.net"`,
		`nonce="abcdefghijklmnopqrstuvwxyz="`,
		`opaque="ALU:abcdefghijklmnopqrstuvwxyz__"`,
		"algorithm=AKAv1-MD5",
		`qop="auth"`,
	} {
		assert.Contains(t, authStr, expect, "folded portion %q missing from unfolded header", expect)
	}
}

// Smaller, deterministic test cases isolating just the folding behavior on a
// header that doesn't have a custom parser (Server is rendered as a generic
// header, so the value round-trips literally).
func TestParseFoldedHeaders_Cases(t *testing.T) {
	cases := []struct {
		name      string
		header    string
		want      string // expected unfolded Value()
	}{
		{
			name: "single_continuation_with_spaces",
			header: "Server: Alcatel-Lucent\r\n" +
				"   HPSS/3.0.3",
			want: "Alcatel-Lucent HPSS/3.0.3",
		},
		{
			name: "single_continuation_with_tab",
			header: "Server: Alcatel-Lucent\r\n" +
				"\tHPSS/3.0.3",
			want: "Alcatel-Lucent HPSS/3.0.3",
		},
		{
			name: "no_folding_baseline",
			header: "Server: Alcatel-Lucent-HPSS/3.0.3",
			want: "Alcatel-Lucent-HPSS/3.0.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rawMsg := []string{
				"SIP/2.0 200 OK",
				"Call-ID: x",
				"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK1",
				"To: <sip:a@b>",
				"From: <sip:c@d>;tag=1",
				"CSeq: 1 OPTIONS",
				tc.header,
				"Content-Length: 0",
				"",
				"",
			}
			data := []byte(strings.Join(rawMsg, "\r\n"))
			parser := NewParser()
			msg, err := parser.ParseSIP(data)
			require.NoError(t, err)
			r := msg.(*Response)
			servers := r.GetHeaders("Server")
			require.NotEmpty(t, servers, "Server header must be present")
			assert.Equal(t, tc.want, servers[0].Value())
		})
	}
}
