package sip

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportLayerClosing(t *testing.T) {
	// NOTE it creates real network connection

	// TODO add other transports
	for _, tran := range []string{TransportUDP} {
		t.Run(tran, func(t *testing.T) {
			tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
			req := NewRequest(OPTIONS, &Uri{Host: "localhost", Port: 5066})
			req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

			conn, err := tp.ClientRequestConnection(context.TODO(), req)
			require.NoError(t, err)

			tp.Close()
			c := conn.(*UDPConnection)
			require.Error(t, c.Close(), "It is not closed already")
		})
	}
}

func TestTransportLayerConnectionReuse(t *testing.T) {
	// NOTE it creates real network connection
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	require.True(t, tp.ConnectionReuse)

	req := NewRequest(OPTIONS, &Uri{Host: "localhost", Port: 5066})
	req.AppendHeader(&ViaHeader{Host: "127.0.0.1", Port: 0, Params: NewParams()})

	conn, err := tp.ClientRequestConnection(context.TODO(), req)
	require.NoError(t, err)

	conn2, err := tp.ClientRequestConnection(context.TODO(), req)
	require.NoError(t, err)
	require.Equal(t, conn, conn2)
}
