package sip

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/gobwas/ws/wsutil"
	"github.com/stretchr/testify/require"
)

func TestTransportLayerWSParseErrorClosesConnection(t *testing.T) {
	for _, transport := range []string{"WS", "WSS"} {
		for _, test := range []struct {
			name string
			data string
		}{
			{"invalid start line", "not a SIP message\r\n\r\n"},
			{"invalid content length", "OPTIONS sip:example.com SIP/2.0\r\nContent-Length: invalid\r\n\r\n"},
			{"incomplete body", "OPTIONS sip:example.com SIP/2.0\r\nContent-Length: 10\r\n\r\nx"},
		} {
			t.Run(transport+"/"+test.name, func(t *testing.T) {
				tran := &TransportWS{}
				tran.init(NewParser())
				tran.transport = transport

				server, client := net.Pipe()
				defer server.Close()
				defer client.Close()
				require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))

				conn := tran.newConnection(server, 1, false)
				msgs := make(chan Message, 2)
				done := make(chan struct{})
				go func() {
					defer close(done)
					tran.readConnection(conn, server.LocalAddr().String(), server.RemoteAddr().String(), func(msg Message) {
						msgs <- msg
					})
				}()

				// WebSocket message boundaries allow SIP without Content-Length.
				require.NoError(t, wsutil.WriteClientText(client, []byte("OPTIONS sip:example.com SIP/2.0\r\n\r\n")))
				select {
				case msg := <-msgs:
					require.Equal(t, transport, msg.Transport())
					require.Equal(t, server.RemoteAddr().String(), msg.Source())
				case <-time.After(2 * time.Second):
					t.Fatal("expected valid WebSocket SIP message")
				}

				require.NoError(t, wsutil.WriteClientText(client, []byte(test.data)))
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("expected SIP parse error to stop the WebSocket read connection")
				}
				select {
				case msg := <-msgs:
					t.Fatalf("handler called with invalid message: %s", msg.String())
				default:
				}
				_, err := client.Read(make([]byte, 1))
				require.ErrorIs(t, err, io.EOF)
			})
		}
	}
}
