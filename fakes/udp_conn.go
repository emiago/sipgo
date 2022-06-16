package fakes

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
)

type UDPConn struct {
	net.UDPConn
	LAddr net.UDPAddr
	RAddr net.UDPAddr

	Reader  io.Reader
	Writers map[string]io.Writer

	mu sync.Mutex
}

func (c *UDPConn) ExpectAddr(addr net.UDPAddr) {
	c.mu.Lock()
	c.RAddr = addr
	c.mu.Unlock()
}

func (c *UDPConn) LocalAddr() net.Addr {
	return &c.LAddr
}

func (c *UDPConn) RemoteAddr() net.Addr {
	return &c.RAddr
}

// This is connection implementation
func (c *UDPConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	c.mu.Lock()
	addr = &net.UDPAddr{
		IP:   c.RAddr.IP,
		Port: c.RAddr.Port,
	}

	n, err = c.Reader.Read(p)
	c.mu.Unlock()
	return n, addr, err
}

// This is connection implementation
func (c *UDPConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	a := addr.String()
	w, exists := c.Writers[a]
	if !exists {
		return 0, fmt.Errorf("non existing writer")
	}

	return w.Write(p)
}

func (c *UDPConn) TestReadConn(t testing.TB) []byte {
	buffer := make([]byte, 65355)
	// var buffer [65355]byte
	n, _, err := c.ReadFrom(buffer)
	if err != nil {
		if err != io.EOF {
			t.Fatal(err)
		}
	}

	if n == 0 {
		t.Fatal("No byte received")
	}
	return buffer[:n]
}

func (c *UDPConn) TestWriteConn(t testing.TB, data []byte) {
	c.mu.Lock()
	addr := &net.UDPAddr{
		IP:   c.RAddr.IP,
		Port: c.RAddr.Port,
	}
	c.mu.Unlock()
	num, err := c.WriteTo(data, addr)

	if err != nil {
		t.Fatal(err)
	}

	if num != len(data) {
		t.Fatal("Data not fully written")
	}
}

func (c *UDPConn) TestRequest(t testing.TB, data []byte) []byte {
	c.TestWriteConn(t, data)
	return c.TestReadConn(t)
}
