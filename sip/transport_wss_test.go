package sip

import (
	"crypto/tls"
	"net"
	"testing"
)

func TestTransportLayerWSDialPath(t *testing.T) {
	l := NewTransportLayer(net.DefaultResolver, NewParser(), nil, WithTransportLayerWSDialPath("/ws"))

	if got := l.wss.DialURI("localhost:443"); got != "wss://localhost:443/ws" {
		t.Fatalf("wss: expected wss://localhost:443/ws, got %q", got)
	}
	if got := l.ws.DialURI("localhost:80"); got != "ws://localhost:80/ws" {
		t.Fatalf("ws: expected ws://localhost:80/ws, got %q", got)
	}
}

func TestTransportLayerWSDialPathUnsetKeepsRoot(t *testing.T) {
	l := NewTransportLayer(net.DefaultResolver, NewParser(), nil)

	if got := l.wss.DialURI("localhost:443"); got != "wss://localhost:443" {
		t.Fatalf("expected wss://localhost:443, got %q", got)
	}
}

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
