package fakes

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type TCPConn struct {
	net.Conn
	LAddr net.TCPAddr
	RAddr net.TCPAddr

	Reader io.Reader
	Writer io.Writer

	mu sync.Mutex

	writeDeadlines []time.Time
	readDeadlines  []time.Time
}

// SetWriteDeadline records the deadline instead of enforcing it. The embedded
// net.Conn is nil on this fake, so the call must not be forwarded.
func (c *TCPConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadlines = append(c.writeDeadlines, t)
	c.mu.Unlock()
	return nil
}

// WriteDeadlines returns the deadlines passed to SetWriteDeadline so far.
func (c *TCPConn) WriteDeadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.writeDeadlines...)
}

// SetReadDeadline records the deadline instead of enforcing it. The embedded
// net.Conn is nil on this fake, so the call must not be forwarded.
func (c *TCPConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadlines = append(c.readDeadlines, t)
	c.mu.Unlock()
	return nil
}

// ReadDeadlines returns the deadlines passed to SetReadDeadline so far.
func (c *TCPConn) ReadDeadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.readDeadlines...)
}

// func (c *TCPConn) ExpectAddr(addr net.TCPAddr) {
// 	c.mu.Lock()
// 	c.RAddr = addr
// 	c.mu.Unlock()
// }

func (c *TCPConn) LocalAddr() net.Addr {
	return &c.LAddr
}

func (c *TCPConn) RemoteAddr() net.Addr {
	return &c.RAddr
}

// This is connection implementation
func (c *TCPConn) Read(p []byte) (n int, err error) {
	// c.mu.Lock()
	// defer c.mu.Unlock()
	n, err = c.Reader.Read(p)
	return n, err
}

// This is connection implementation
func (c *TCPConn) Write(p []byte) (n int, err error) {
	// c.mu.Lock()
	// defer c.mu.Unlock()
	return c.Writer.Write(p)
}

func (c *TCPConn) Close() error {
	return nil
}

func (c *TCPConn) TestReadConn(t testing.TB) []byte {
	buffer := make([]byte, 65355)
	// var buffer [65355]byte
	n, err := c.Read(buffer)
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

func (c *TCPConn) TestWriteConn(t testing.TB, data []byte) {
	num, err := c.Write(data)

	if err != nil {
		t.Fatal(err)
	}

	if num != len(data) {
		t.Fatal("Data not fully written")
	}
}

func (c *TCPConn) TestRequest(t testing.TB, data []byte) []byte {
	c.TestWriteConn(t, data)
	return c.TestReadConn(t)
}

type TCPListener struct {
	LAddr net.TCPAddr
	Conns chan *TCPConn
}

// Accept waits for and returns the next connection to the listener.
func (c *TCPListener) Accept() (net.Conn, error) {
	return <-c.Conns, nil
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (c *TCPListener) Close() error {
	return nil
}

// Addr returns the listener's network address.
func (c *TCPListener) Addr() net.Addr {
	return &c.LAddr
}
