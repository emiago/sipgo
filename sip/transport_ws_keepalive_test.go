package sip

import (
	"net"
	"testing"
	"time"

	"github.com/gobwas/ws/wsutil"
	"github.com/stretchr/testify/require"
)

func TestTransportLayerWSCRLFKeepAlive(t *testing.T) {
	tran := &TransportWS{}
	tran.init(NewParser())

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))

	go tran.initConnection(server, server.RemoteAddr().String(), false, func(Message) {})

	require.NoError(t, wsutil.WriteClientText(client, []byte("\r\n\r\n")))
	pong, err := wsutil.ReadServerText(client)
	require.NoError(t, err)
	require.Equal(t, "\r\n", string(pong))
}
