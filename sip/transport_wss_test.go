package sip

import (
	"crypto/tls"
	"testing"
)

func TestTransportWSSDialURI(t *testing.T) {
	t.Run("defaults to wss scheme", func(t *testing.T) {
		tran := &TransportWSS{TransportWS: &TransportWS{}}
		tran.init(NewParser(), &tls.Config{})

		got := tran.DialURI("localhost:443")
		if got != "wss://localhost:443" {
			t.Fatalf("expected wss://localhost:443, got %q", got)
		}
	})

	t.Run("caller DialURI is kept", func(t *testing.T) {
		tran := &TransportWSS{TransportWS: &TransportWS{
			DialURI: func(host string) string { return "wss://" + host + "/ws" },
		}}
		tran.init(NewParser(), &tls.Config{})

		got := tran.DialURI("localhost:443")
		if got != "wss://localhost:443/ws" {
			t.Fatalf("expected wss://localhost:443/ws, got %q", got)
		}
	})
}
