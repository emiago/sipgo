package parser

import (
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func FuzzParseSipMessage(f *testing.F) {
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}).With().Timestamp().Logger().Level(zerolog.WarnLevel)

	if lvl, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		log.Logger = log.Level(lvl)
	}

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
