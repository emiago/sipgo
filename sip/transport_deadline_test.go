package sip

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestTransportTimeoutDefaultDisabled(t *testing.T) {
	layer := NewTransportLayer(net.DefaultResolver, NewParser(), nil)
	t.Cleanup(func() { _ = layer.Close() })

	if layer.tcp.ReadTimeout != 0 || layer.tcp.WriteTimeout != 0 {
		t.Fatalf("default TCP timeouts = (%v, %v), want both disabled", layer.tcp.ReadTimeout, layer.tcp.WriteTimeout)
	}
	if layer.ws.ReadTimeout != 0 || layer.ws.WriteTimeout != 0 {
		t.Fatalf("default WS timeouts = (%v, %v), want both disabled", layer.ws.ReadTimeout, layer.ws.WriteTimeout)
	}
}

func TestTransportTimeoutConfig(t *testing.T) {
	tcp := &TransportTCP{ReadTimeout: time.Second, WriteTimeout: 2 * time.Second}
	tlsTCP := &TransportTCP{ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second}
	ws := &TransportWS{ReadTimeout: 5 * time.Second, WriteTimeout: 6 * time.Second}
	wssWS := &TransportWS{ReadTimeout: 7 * time.Second, WriteTimeout: 8 * time.Second}

	layer := NewTransportLayer(net.DefaultResolver, NewParser(), nil, WithTransportLayerTransports(TransportsConfig{
		TCP: tcp,
		TLS: &TransportTLS{TransportTCP: tlsTCP},
		WS:  ws,
		WSS: &TransportWSS{TransportWS: wssWS},
	}))
	t.Cleanup(func() { _ = layer.Close() })

	tests := []struct {
		name         string
		timeouts     func() (time.Duration, time.Duration)
		readTimeout  time.Duration
		writeTimeout time.Duration
	}{
		{"TCP", func() (time.Duration, time.Duration) {
			conn := layer.tcp.newConnection(nil, 0)
			return conn.readTimeout, conn.writeTimeout
		}, time.Second, 2 * time.Second},
		{"TLS", func() (time.Duration, time.Duration) {
			conn := layer.tls.newConnection(nil, 0)
			return conn.readTimeout, conn.writeTimeout
		}, 3 * time.Second, 4 * time.Second},
		{"WS", func() (time.Duration, time.Duration) {
			conn := layer.ws.newConnection(nil, 0, false)
			return conn.readTimeout, conn.writeTimeout
		}, 5 * time.Second, 6 * time.Second},
		{"WSS", func() (time.Duration, time.Duration) {
			conn := layer.wss.newConnection(nil, 0, false)
			return conn.readTimeout, conn.writeTimeout
		}, 7 * time.Second, 8 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readTimeout, writeTimeout := test.timeouts()
			if readTimeout != test.readTimeout {
				t.Fatalf("read timeout = %v, want %v", readTimeout, test.readTimeout)
			}
			if writeTimeout != test.writeTimeout {
				t.Fatalf("write timeout = %v, want %v", writeTimeout, test.writeTimeout)
			}
		})
	}
}

func TestStreamConnectionReadTimeout(t *testing.T) {
	tests := []struct {
		name string
		new  func(net.Conn) interface{ Read([]byte) (int, error) }
	}{
		{
			name: "TCP",
			new: func(conn net.Conn) interface{ Read([]byte) (int, error) } {
				return (&TransportTCP{ReadTimeout: 25 * time.Millisecond}).newConnection(conn, 1)
			},
		},
		{
			name: "WS",
			new: func(conn net.Conn) interface{ Read([]byte) (int, error) } {
				return (&TransportWS{ReadTimeout: 25 * time.Millisecond}).newConnection(conn, 1, false)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()

			done := make(chan error, 1)
			go func() {
				_, err := test.new(local).Read(make([]byte, TransportBufferReadSize))
				done <- err
			}()

			select {
			case err := <-done:
				requireDeadlineExceeded(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("read did not stop at its configured timeout")
			}
		})
	}
}

func TestStreamConnectionWriteTimeout(t *testing.T) {
	tests := []struct {
		name string
		new  func(net.Conn) interface{ Write([]byte) (int, error) }
	}{
		{
			name: "TCP",
			new: func(conn net.Conn) interface{ Write([]byte) (int, error) } {
				return (&TransportTCP{WriteTimeout: 25 * time.Millisecond}).newConnection(conn, 1)
			},
		},
		{
			name: "WS",
			new: func(conn net.Conn) interface{ Write([]byte) (int, error) } {
				return (&TransportWS{WriteTimeout: 25 * time.Millisecond}).newConnection(conn, 1, true)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close() // The peer intentionally does not read.

			done := make(chan error, 1)
			go func() {
				_, err := test.new(local).Write([]byte("OPTIONS sip:example.com SIP/2.0\r\n"))
				done <- err
			}()

			select {
			case err := <-done:
				requireDeadlineExceeded(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("write did not stop at its configured timeout")
			}
		})
	}
}

func requireDeadlineExceeded(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a deadline error")
	}

	var netErr net.Error
	if !errors.Is(err, os.ErrDeadlineExceeded) && !(errors.As(err, &netErr) && netErr.Timeout()) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
}
