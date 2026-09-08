package sip

import (
	"strings"
	"testing"
)

func FuzzParseSipMessage(f *testing.F) {
	// Creates Race when run
	rawMsg := []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.2:5060;branch=z9hG4bK.VYWrxJJyeEJfngAjKXELr8aPYuX8tR22",
		"From: \"Alice\" <sip:alice@127.0.0.2:5060>;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Contact: <sip:alice@127.0.0.2:5060;expires=3600>",
		"Content-Type: application/sdp",
		"Content-Length: 0",
		"",
	}

	f.Add(strings.Join(rawMsg, "\r\n"))

	parser := NewParser()

	f.Fuzz(func(t *testing.T, orig string) {
		parser.ParseSIP([]byte(orig))
	})
}
