package transport

import (
	"net"
	"testing"
)

func TestConnectionPool(t *testing.T) {
	pool := NewConnectionPool()
	conn := &conn{&net.TCPConn{}}

	a := &net.TCPAddr{
		IP:   net.IPv4('1', '2', '3', '4'),
		Port: 1000,
	}
	pool.Add(a.String(), conn)

	a2 := &net.TCPAddr{
		IP:   net.IPv4('1', '2', '3', '4'),
		Port: 1000,
	}
	c := pool.Get(a2.String())
	if c != conn {
		t.Fatal("Not found connection")
	}
}

func BenchmarkConnectionPool(b *testing.B) {
	pool := NewConnectionPool()
	for i := 0; i < b.N; i++ {
		conn := &conn{&net.TCPConn{}}
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

func BenchmarkTCPPool(b *testing.B) {
	pool := NewTCPPool()
	for i := 0; i < b.N; i++ {
		conn := &net.TCPConn{}
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
