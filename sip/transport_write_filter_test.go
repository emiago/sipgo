package sip

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testParsedOptions(t *testing.T, callID string) Message {
	t.Helper()
	msg, err := NewParser().ParseSIP(testRawOptions(callID))
	require.NoError(t, err)
	return msg
}

func TestTransportLayerWriteFilterUDP(t *testing.T) {
	filterCalls := make(chan TransportWriteProps, 1)
	tp := NewTransportLayer(net.DefaultResolver, NewParser(), nil, WithTransportLayerWriteFilter(
		func(info TransportWriteProps, data []byte) ([]byte, error) {
			filterCalls <- info
			return bytes.ReplaceAll(data, []byte("bad-call"), []byte("good-call")), nil
		},
	))
	defer tp.Close()

	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer serverConn.Close()
	go func() {
		_ = tp.ServeUDP(serverConn)
	}()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer peer.Close()

	// Wait for the listener to be registered before writing through it.
	require.Eventually(t, func() bool { return tp.GetListenPort("udp") != 0 }, 2*time.Second, 10*time.Millisecond)
	peerAddr := peer.LocalAddr().(*net.UDPAddr)
	req := NewRequest(OPTIONS, Uri{Host: "127.0.0.1", Port: peerAddr.Port})
	req.AppendHeader(&ViaHeader{ProtocolName: "SIP", ProtocolVersion: "2.0", Transport: "UDP", Host: "127.0.0.1", Params: NewParams()})
	callID := CallIDHeader("bad-call")
	req.AppendHeader(&callID)
	conn, err := tp.ClientRequestConnection(context.Background(), req)
	require.NoError(t, err)
	defer conn.TryClose()
	require.NoError(t, conn.WriteMsg(req))

	buf := make([]byte, 2048)
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(2*time.Second)))
	n, _, err := peer.ReadFrom(buf)
	require.NoError(t, err)
	require.Contains(t, string(buf[:n]), "Call-ID: good-call")

	info := <-filterCalls
	require.Equal(t, "UDP", info.Transport)
	require.Equal(t, peer.LocalAddr().String(), info.RemoteAddr.String())
	require.NotNil(t, info.LocalAddr)
}

func TestTransportWriteFilterTCP(t *testing.T) {
	filterCalls := make(chan TransportWriteProps, 1)
	tcp := &TransportTCP{
		writeFilter: func(info TransportWriteProps, data []byte) ([]byte, error) {
			filterCalls <- info
			return data, nil
		},
	}
	tcp.init(NewParser())

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	c := tcp.newConnection(serverConn, 1)
	defer c.Close()

	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 2048)
		n, _ := clientConn.Read(buf)
		got <- buf[:n]
	}()
	require.NoError(t, c.WriteMsg(testParsedOptions(t, "tcp-call")))
	require.Contains(t, string(<-got), "Call-ID: tcp-call")

	info := <-filterCalls
	require.Equal(t, "TCP", info.Transport)
	require.Equal(t, clientConn.LocalAddr(), info.RemoteAddr)
}

func TestTransportWriteFilterErrorDropsMessage(t *testing.T) {
	filterErr := errors.New("drop write")
	tcp := &TransportTCP{
		writeFilter: func(TransportWriteProps, []byte) ([]byte, error) { return nil, filterErr },
	}
	tcp.init(NewParser())

	serverConn, clientConn := net.Pipe()
	c := tcp.newConnection(serverConn, 1)
	require.ErrorIs(t, c.WriteMsg(testParsedOptions(t, "dropped")), filterErr)

	// Nothing reached the wire: closing the writer side ends the read with EOF and no data.
	require.NoError(t, serverConn.Close())
	data, err := io.ReadAll(clientConn)
	require.NoError(t, err)
	require.Empty(t, data)
}
