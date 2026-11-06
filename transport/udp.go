package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	sipgo "github.com/emiago/sipgo/sip"

	"github.com/livekit/sipgo/sip"
)

var (
	// UDPReadWorkers defines how many listeners will work
	// Best performance is achieved with low value, to remove high concurency
	UDPReadWorkers int = 1

	UDPMTUSize = 1500

	ErrUDPMTUCongestion = errors.New("size of packet larger than MTU")
)

// UDP transport implementation
type UDPTransport struct {
	// listener *net.UDPConn
	parser *sipgo.Parser

	pool      *ConnectionPool
	mu        sync.Mutex
	listeners []*UDPConnection

	log *slog.Logger
}

func NewUDPTransport(log *slog.Logger, par *sipgo.Parser) *UDPTransport {
	p := &UDPTransport{
		parser: par,
		pool:   NewConnectionPool(),
	}
	p.log = log.With("caller", "transport<UDP>")
	return p
}

func (t *UDPTransport) String() string {
	return "transport<UDP>"
}

func (t *UDPTransport) Network() string {
	return TransportUDP
}

func (t *UDPTransport) Close() error {
	t.pool.Clear(t.log)
	return nil
}

// ServeConn is direct way to provide conn on which this worker will listen
// UDPReadWorkers are used to create more workers
func (t *UDPTransport) Serve(conn net.PacketConn, handler sip.MessageHandler) error {
	t.log.Debug("begin listening on", "net", t.Network(), "addr", conn.LocalAddr())
	/*
		Multiple readers makes problem, which can delay writing response
	*/

	c := &UDPConnection{PacketConn: conn, log: t.log}

	t.mu.Lock()
	t.listeners = append(t.listeners, c)
	t.mu.Unlock()

	for i := 0; i < UDPReadWorkers-1; i++ {
		go t.readConnection(c, handler)
	}
	t.readConnection(c, handler)

	return nil
}

func (t *UDPTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveUDPAddr("udp", addr)
}

// GetConnection will return same listener connection
func (t *UDPTransport) GetConnection(addr string) (Connection, error) {
	// Single udp connection as listener can only be used as long IP of a packet in same network
	// In case this is not the case we should return error?
	// https://dadrian.io/blog/posts/udp-in-go/

	// Pool must be checked as it can be Client mode only and connection is created
	if conn := t.pool.Get(addr); conn != nil {
		return conn, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	// TODO: How to pick listener. Some address range mapping
	if len(t.listeners) > 0 {
		return t.listeners[0], nil
	}

	return nil, nil
}

// CreateConnection will create new connection
func (t *UDPTransport) CreateConnection(laddr Addr, host string, raddr Addr, handler sip.MessageHandler) (Connection, error) {
	// raddr, err := net.ResolveUDPAddr("udp", addr)
	// if err != nil {
	// 	return nil, err
	// }

	var uladdr *net.UDPAddr = nil
	if laddr.IP != nil {
		uladdr = &net.UDPAddr{
			IP:   laddr.IP,
			Port: laddr.Port,
		}
	}

	uraddr := &net.UDPAddr{
		IP:   raddr.IP,
		Port: raddr.Port,
	}

	udpconn, err := net.ListenUDP("udp", uladdr)
	if err != nil {
		return nil, err
	}

	c := &UDPConnection{
		PacketConn: udpconn,
		raddr:      uraddr,
		log:        t.log,
		refcount:   2, // The pool's and the dialer's.
	}

	addr := uraddr.String()
	t.log.Debug("New connection", "raddr", addr)

	// Wrap it in reference
	t.pool.Add(addr, c)
	go t.readConnectedConnection(c, handler)
	return c, err
}

func (t *UDPTransport) readConnection(conn *UDPConnection, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer conn.Close()
	for {
		num, raddr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				t.log.Debug("Read connection closed", "err", err)
				return
			}
			t.log.Error("Read connection error", "err", err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr.String(), handler)
	}
}

func (t *UDPTransport) readConnectedConnection(conn *UDPConnection, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)
	raddr := conn.raddr.String()
	defer t.pool.CloseAndDelete(t.log, conn, raddr)

	for {
		num, err := conn.Read(buf)

		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug("Read connection closed", "err", err)
				return
			}
			t.log.Error("Read connection error", "err", err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr, handler)
	}
}

// This should performe better to avoid any interface allocation
// For now no usage, but leaving here
func (t *UDPTransport) readUDPConn(conn *net.UDPConn, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer conn.Close()

	for {
		//ReadFromUDP should make one less allocation
		num, raddr, err := conn.ReadFromUDP(buf)

		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				t.log.Debug("Read connection closed", "err", err)
				return
			}
			t.log.Error("Read UDP connection error", "err", err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr.String(), handler)
	}
}

func (t *UDPTransport) parseAndHandle(data []byte, src string, handler sip.MessageHandler) {
	// Check is keep alive
	if len(data) <= 4 {
		//One or 2 CRLF
		if len(bytes.Trim(data, "\r\n")) == 0 {
			t.log.Debug("Keep alive CRLF received")
			return
		}
	}

	bytesPacketSize.WithLabelValues("udp", "read").Observe(float64(len(data)))

	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Info("failed to parse", "err", err, "data", string(data))
		return
	}

	msg.SetTransport(TransportUDP)
	msg.SetSource(src)
	handler(msg)
}

type UDPConnection struct {
	// mutual exclusive for now
	// TODO Refactor
	PacketConn net.PacketConn
	raddr      *net.UDPAddr
	log        *slog.Logger

	mu       sync.RWMutex
	refcount int
	// closed is set once the conn is closed. Holders may still release their
	// references afterwards, so refcount is left alone.
	closed bool
}

func (c *UDPConnection) LocalAddr() net.Addr {
	return c.PacketConn.LocalAddr()
}

func (c *UDPConnection) Ref(i int) int {
	// For now all udp connections must be reused
	if c.raddr == nil {
		return 0
	}

	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	return ref
}

func (c *UDPConnection) Close() error {
	// TODO closing packet connection is problem
	// but maybe referece could help?
	if c.raddr == nil {
		return nil
	}
	c.mu.Lock()
	closed := c.closed
	c.closed = true
	ref := c.refcount
	c.mu.Unlock()
	if closed {
		return nil
	}
	c.log.Debug("UDP doing hard close", "ip", c.LocalAddr(), "dst", c.raddr, "ref", ref)
	return c.PacketConn.Close()
}

func (c *UDPConnection) TryClose() (int, error) {
	if c.raddr == nil {
		return 0, nil
	}

	c.mu.Lock()
	c.refcount--
	ref, closed := c.refcount, c.closed
	if ref == 0 {
		c.closed = true
	}
	c.mu.Unlock()
	c.log.Debug("UDP reference decrement", "src", c.PacketConn.LocalAddr(), "dst", c.raddr, "ref", ref)
	if ref < 0 {
		c.log.Warn("UDP ref went negative", "src", c.PacketConn.LocalAddr(), "dst", c.raddr, "ref", ref)
		return 0, nil
	}
	if ref > 0 {
		return ref, nil
	}
	if closed {
		return 0, nil
	}

	c.log.Debug("UDP closing", "ip", c.LocalAddr(), "dst", c.raddr, "ref", ref)
	return 0, c.PacketConn.Close()
}

func (c *UDPConnection) Read(b []byte) (n int, err error) {
	n, _, err = c.PacketConn.ReadFrom(b)
	if SIPDebug {
		c.log.Debug("UDP read", "local", c.PacketConn.LocalAddr(), "remote", c.raddr, "data", string(b[:n]))
	}
	return n, err
}

func (c *UDPConnection) Write(b []byte) (n int, err error) {
	if SIPDebug {
		c.log.Debug("UDP write", "local", c.PacketConn.LocalAddr(), "remote", c.raddr, "data", string(b))
	}
	return c.PacketConn.WriteTo(b, c.raddr)
}

func (c *UDPConnection) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	// Some debug hook. TODO move to proper way
	n, addr, err = c.PacketConn.ReadFrom(b)
	if err == nil && SIPDebug {
		c.log.Debug("UDP read", "local", c.PacketConn.LocalAddr(), "remote", addr, "data", string(b[:n]))
	}
	return n, addr, err
}

func (c *UDPConnection) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.PacketConn.WriteTo(b, addr)
	if SIPDebug {
		c.log.Debug("UDP write", "local", c.PacketConn.LocalAddr(), "remote", addr, "data", string(b))
	}
	return n, err
}

func (c *UDPConnection) WriteMsg(msg sip.Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	bytesPacketSize.WithLabelValues("udp", "write").Observe(float64(len(data)))

	if len(data) > UDPMTUSize-200 {
		return ErrUDPMTUCongestion
	}

	var n int
	// TODO doing without if
	if c.raddr != nil {
		var err error
		n, err = c.Write(data)
		if err != nil {
			return fmt.Errorf("conn %s write err=%w", c.PacketConn.LocalAddr().String(), err)
		}
	} else {
		var err error

		// TODO lets return this better
		dst := msg.Destination() // Destination should be already resolved by transport layer
		host, port, err := sip.ParseAddr(dst)
		if err != nil {
			return err
		}
		raddr := net.UDPAddr{
			IP:   net.ParseIP(host),
			Port: port,
		}

		n, err = c.WriteTo(data, &raddr)
		if err != nil {
			return fmt.Errorf("udp conn %s err. %w", c.PacketConn.LocalAddr().String(), err)
		}
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}
