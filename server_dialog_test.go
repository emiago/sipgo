package sipgo

import (
	"io"
	"net"
	"testing"

	"github.com/emiago/sipgo/fakes"
	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialog(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	srv, err := NewServerDialog(ua)
	require.Nil(t, err)

	serverReader, serverWriter := io.Pipe()
	client1Reader, client1Writer := io.Pipe()
	// _, client2Writer := io.Pipe()
	//Client1 writes to server and reads response

	serverAddr := net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	// client2Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}
	client1 := &fakes.UDPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
		Writers: map[string]io.Writer{
			serverAddr.String(): serverWriter,
		},
	}

	// //Client2 writes to server and reads response
	// client2 := &fakes.UDPConn{
	// 	LAddr:  client2Addr,
	// 	RAddr:  serverAddr,
	// 	Reader: client2Reader,
	// 	Writers: map[string]io.Writer{
	// 		serverAddr.String(): serverWriter,
	// 	},
	// }

	//Server writes to clients and reads response
	serverC := &fakes.UDPConn{
		LAddr:  serverAddr,
		RAddr:  client1Addr,
		Reader: serverReader,
		Writers: map[string]io.Writer{
			client1Addr.String(): client1Writer,
			// client2Addr.String(): client2Writer,
		},
	}

	go srv.TransportLayer().ServeUDP(serverC)

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		t.Log("New INVITE request")
		// Make all responses
		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		tx.Respond(res)
	})

	ch := make(chan sip.Dialog)
	srv.OnDialogChan(ch)

	inviteReq, callid, ftag := createTestInvite(t, "sip:bob@127.0.0.1:5060", "UDP", client1.LocalAddr().String())
	client1.TestWriteConn(t, []byte(inviteReq.String()))

	d := <-ch
	assert.Equal(t, sip.DialogStateEstablished, d.State)

	byeReq := createTestBye(t, "sip:bob@127.0.0.1:5060", "UDP", client1.LocalAddr().String(), callid, ftag, ftag)
	client1.TestWriteConn(t, []byte(byeReq.String()))

	d = <-ch
	assert.Equal(t, sip.DialogStateEnded, d.State)
}
