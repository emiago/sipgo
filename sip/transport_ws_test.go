package sip

import (
	"bytes"
	"errors"
	"net"
	"testing"

	"github.com/emiago/sipgo/fakes"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/stretchr/testify/require"
)

func TestWSConnectionReadRejectsOversizedFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		_, _ = client.Write([]byte{
			0x81,
			0xff,
			0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0x00, 0x00, 0x00, 0x00,
		})
	}()

	conn := &WSConnection{Conn: server}
	_, err := conn.Read(make([]byte, TransportBufferReadSize))
	if !errors.Is(err, wsutil.ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestWSConnectionReadControlFrames(t *testing.T) {
	for _, mode := range []struct {
		name       string
		clientSide bool
		fragmented bool
	}{
		{"server", false, false},
		{"client", true, false},
		{"server fragmented", false, true},
		{"client fragmented", true, true},
	} {
		for _, payload := range []string{"", "keepalive", string(bytes.Repeat([]byte("x"), 125))} {
			t.Run(mode.name+"/"+payload, func(t *testing.T) {
				var input, output bytes.Buffer
				write := func(frame ws.Frame) {
					frame.Payload = bytes.Clone(frame.Payload)
					if !mode.clientSide {
						frame = ws.MaskFrameInPlace(frame)
					}
					require.NoError(t, ws.WriteFrame(&input, frame))
				}
				message := testRawOptions("control-frame")
				if mode.fragmented {
					write(ws.NewFrame(ws.OpText, false, message[:10]))
				}
				write(ws.NewPingFrame([]byte(payload)))
				write(ws.NewPongFrame([]byte(payload)))
				if mode.fragmented {
					write(ws.NewFrame(ws.OpContinuation, true, message[10:]))
				} else {
					write(ws.NewTextFrame(message))
				}
				write(ws.NewTextFrame(message))

				conn := &WSConnection{
					Conn:       &fakes.TCPConn{Reader: &input, Writer: &output},
					clientSide: mode.clientSide,
				}
				buf := make([]byte, TransportBufferReadSize)
				for range 2 {
					n, err := conn.Read(buf)
					require.NoError(t, err)
					require.Equal(t, message, buf[:n])
				}
				pong, err := ws.ReadFrame(&output)
				require.NoError(t, err)
				require.Equal(t, ws.OpPong, pong.Header.OpCode)
				require.True(t, pong.Header.Fin)
				require.Equal(t, mode.clientSide, pong.Header.Masked)
				pong = ws.UnmaskFrameInPlace(pong)
				require.Equal(t, payload, string(pong.Payload))
				require.Zero(t, output.Len(), "unsolicited pong must not receive a reply")
			})
		}
	}
}
