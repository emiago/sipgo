package sip

import (
	"net"
	"sync"
	"testing"

	"github.com/emiago/sipgo/fakes"
)

func TestConnectionPool(t *testing.T) {
	pool := newConnectionPool()

	fakeConn := &fakes.TCPConn{
		LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
		Reader: nil,
		Writer: nil,
	}
	conn := &TCPConnection{Conn: fakeConn}

	pool.Add(fakeConn.RAddr.String(), conn)

	c := pool.Get(fakeConn.RAddr.String())
	if c != conn {
		t.Fatal("Not found connection")
	}
}

func TestTransportLayerPool(t *testing.T) {
	layer := NewTransportLayer(nil, NewParser(), nil)
	conn := poolTestConnection()
	layer.tcp.pool.Add("local", conn)
	layer.tcp.pool.Add("remote", conn)
	layer.udp.pool.Add("udp", &UDPConnection{})

	tcpPool := layer.Pool("TCP")
	if tcpPool != layer.tcp.pool {
		t.Fatal("TCP pool is not the live transport pool")
	}
	if got := tcpPool.Size(); got != 2 {
		t.Fatalf("TCP address entries: got %d, want 2", got)
	}
	if layer.Pool("udp") != layer.udp.pool {
		t.Fatal("UDP pool is not the live transport pool")
	}
	if got := layer.Pool("udp").Size(); got != 1 {
		t.Fatalf("UDP count: got %d, want 1", got)
	}
	for _, item := range []struct {
		network string
		pool    *ConnectionPool
	}{
		{"tls", layer.tls.pool},
		{"ws", layer.ws.pool},
		{"wss", layer.wss.pool},
	} {
		if got := layer.Pool(item.network); got != item.pool {
			t.Fatalf("%s did not return its live pool", item.network)
		}
		if got := item.pool.Size(); got != 0 {
			t.Fatalf("%s count: got %d, want 0", item.network, got)
		}
	}
	if got := layer.Pool("invalid"); got != nil {
		t.Fatalf("unsupported network returned a pool: %v", got)
	}

	layer.tcp.pool.Add("later", poolTestConnection())
	if got := tcpPool.Size(); got != 3 {
		t.Fatalf("live TCP pool has %d address entries, want 3", got)
	}
}

func TestConnectionPoolSizeDuringConnectionCreation(t *testing.T) {
	pool := newConnectionPool()
	const connections = 32
	done := make(chan struct{})
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-done:
				return
			default:
				pool.Size()
			}
		}
	}()

	var writers sync.WaitGroup
	for i := range connections {
		writers.Add(1)
		go func() {
			defer writers.Done()
			_, err := pool.addSingleflight(
				Addr{IP: net.ParseIP("127.0.0.2"), Port: 5000 + i},
				Addr{}, false,
				func() (Connection, error) { return poolTestConnection(), nil },
			)
			if err != nil {
				t.Errorf("create pooled connection: %v", err)
			}
		}()
	}
	writers.Wait()
	close(done)
	reader.Wait()

	if got := pool.Size(); got != connections+1 {
		t.Fatalf("got %d address entries, want %d", got, connections+1)
	}
}

func poolTestConnection() *TCPConnection {
	return &TCPConnection{Conn: &fakes.TCPConn{
		LAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		RAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
	}}
}

func BenchmarkConnectionPool(b *testing.B) {
	pool := newConnectionPool()

	for i := 0; i < b.N; i++ {
		conn := &TCPConnection{Conn: &fakes.TCPConn{
			LAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
			RAddr:  net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060},
			Reader: nil,
			Writer: nil,
		}}
		a := &net.TCPAddr{
			IP:   net.IPv4('1', '2', '3', byte(i)),
			Port: 1000,
		}
		pool.Add(a.String(), conn)
		c := pool.Get(a.String())
		if c != conn {
			b.Fatal("mismatched function")
		}
	}
}
