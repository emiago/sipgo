package sip

import (
	"context"
	"net"
	"testing"

	"github.com/emiago/sipgo/fakes"
	"github.com/stretchr/testify/require"
)

func TestTransportLayerRportFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		params   HeaderParams
		network  string
		viaPort  int
		source   string
		want     string
		rport    string
		received string
	}{
		{
			name: "missing rport uses mapped source port", network: "UDP", viaPort: 5060,
			source: "198.51.100.27:58125", want: "198.51.100.27:58125",
			rport: "58125", received: "198.51.100.27",
		},
		{
			name: "lowercase UDP uses mapped source port", network: "udp", viaPort: 5060,
			source: "198.51.100.27:58125", want: "198.51.100.27:58125",
			rport: "58125", received: "198.51.100.27",
		},
		{
			name: "missing Via port uses source port", network: "UDP",
			source: "198.51.100.27:58125", want: "198.51.100.27:58125",
			rport: "58125", received: "198.51.100.27",
		},
		{
			name: "matching source and Via ports", network: "UDP", viaPort: 5060,
			source: "198.51.100.27:5060", want: "198.51.100.27:5060",
			rport: "5060", received: "198.51.100.27",
		},
		{
			name: "empty rport keeps existing handling", network: "UDP", viaPort: 5060,
			params: HeaderParams{{K: "rport"}},
			source: "198.51.100.27:58125", want: "198.51.100.27:58125",
			rport: "58125", received: "198.51.100.27",
		},
		{
			name: "supplied rport is not overwritten", network: "UDP", viaPort: 5060,
			params: HeaderParams{{K: "rport", V: "9999"}},
			source: "198.51.100.27:58125", want: "198.51.100.27:9999",
			rport: "9999",
		},
		{
			name: "missing rport supports IPv6", network: "UDP", viaPort: 5060,
			source: "[2001:db8::1]:58125", want: "[2001:db8::1]:58125",
			rport: "58125", received: "2001:db8::1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
			t.Cleanup(func() { require.NoError(t, tp.Close()) })

			conn := &UDPConnection{PacketConn: &fakes.UDPConn{}, Listener: true}
			tp.udp.pool.Add(tc.source, conn)

			req := NewRequest(INVITE, Uri{Scheme: "sip", User: "1001", Host: "example.com"})
			req.SetTransport(tc.network)
			req.SetSource(tc.source)
			req.AppendHeader(&ViaHeader{
				ProtocolName: "SIP", ProtocolVersion: "2.0", Transport: "UDP",
				Host: "10.20.0.10", Port: tc.viaPort, Params: tc.params.Clone(),
			})
			lower := &ViaHeader{Host: "10.30.0.10", Port: 5060, Params: HeaderParams{{K: "rport", V: "1234"}}}
			req.AppendHeader(lower)

			called := false
			tp.OnMessage(func(msg Message) {
				called = true
				require.Same(t, req, msg)

				c, err := tp.serverRequestConnection(context.Background(), req)
				require.NoError(t, err)
				defer c.TryClose()
				require.Same(t, conn, c)

				for _, code := range []int{100, 180, 200, 401, 487} {
					res := NewResponseFromRequest(req, code, "Test", nil)
					require.Equal(t, tc.want, res.Destination())
					require.Equal(t, tc.rport, res.Via().Params.GetOr("rport", ""))
					require.Equal(t, tc.received, res.Via().Params.GetOr("received", ""))
				}
			})

			tp.handleMessage(req)
			require.True(t, called)
			require.Equal(t, "1234", lower.Params.GetOr("rport", ""), "only the top Via may change")

			if tc.params.Has("rport") {
				require.Equal(t, tc.params, req.Via().Params, "an existing rport must remain unchanged")
			}
		})
	}
}

func TestTransportLayerRportFallbackIgnoresOtherMessages(t *testing.T) {
	for _, network := range []string{"TCP", "TLS", "WS", "WSS"} {
		t.Run(network, func(t *testing.T) {
			tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
			t.Cleanup(func() { require.NoError(t, tp.Close()) })

			req := NewRequest(OPTIONS, Uri{Host: "example.com"})
			req.SetTransport(network)
			req.SetSource("198.51.100.1:58125")
			req.AppendHeader(&ViaHeader{Host: "10.20.0.10", Port: 5060})
			before := req.String()

			tp.handleMessage(req)
			require.Equal(t, before, req.String())
		})
	}

	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	t.Cleanup(func() { require.NoError(t, tp.Close()) })

	req := NewRequest(OPTIONS, Uri{Host: "example.com"})
	req.SetTransport("UDP")
	req.AppendHeader(&ViaHeader{Host: "10.20.0.10", Port: 5060})
	tp.handleMessage(req)
	require.False(t, req.Via().Params.Has("rport"), "there is no observed source")

	req.SetSource("198.51.100.1:58125")
	req.RemoveHeader("Via")
	require.NotPanics(t, func() { tp.handleMessage(req) })

	res := NewResponse(200, "OK")
	res.SetTransport("UDP")
	res.SetSource("198.51.100.1:58125")
	res.AppendHeader(&ViaHeader{Host: "10.20.0.10", Port: 5060})
	before := res.String()
	tp.handleMessage(res)
	require.Equal(t, before, res.String(), "received responses must not be rewritten")
}
