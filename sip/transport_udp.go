package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
)

var (
	UDPMTUSize = 1500

	ErrUDPMTUCongestion = errors.New("size of packet larger than MTU")
)

// UDP transport implementation
type TransportUDP struct {
	// listener *net.UDPConn
	parser          *Parser
	pool            *connectionPool
	log             *slog.Logger
	connectionReuse bool
}

func (t *TransportUDP) init(par *Parser) {
	t.parser = par
	t.pool = newConnectionPool()
	if t.log == nil {
		t.log = DefaultLogger()
	}
}

func (t *TransportUDP) String() string {
	return "transport<UDP>"
}

func (t *TransportUDP) Network() string {
	return "UDP"
}

func (t *TransportUDP) Close() error {
	return t.pool.Clear()
	// Closing listeners is caller thing.
}

// ServeConn is direct way to provide conn on which this worker will listen
func (t *TransportUDP) Serve(conn net.PacketConn, handler MessageHandler) error {
	t.log.Debug("begin listening", "network", t.Network(), "addr", conn.LocalAddr().String())
	/*
		Multiple readers makes problem, which can delay writing response
	*/
	c := &UDPConnection{
		PacketConn: conn,
		PacketAddr: conn.LocalAddr().String(),
		Listener:   true,
	}

	t.pool.Add(c.PacketAddr, c)
	t.readListenerConnection(c, c.PacketAddr, handler)
	return nil
}

func (t *TransportUDP) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveUDPAddr("udp", addr)
}

// GetConnection will return same listener connection
func (t *TransportUDP) GetConnection(addr string) Connection {
	// Single udp connection as listener can only be used as long IP of a packet in same network
	// In case this is not the case we should return error?
	// https://dadrian.io/blog/posts/udp-in-go/
	// Pool consists either of every new packet From addr or client created connection
	return t.pool.Get(addr)
}

// CreateConnection will create new connection
func (t *TransportUDP) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	return t.createConnection(ctx, laddr, raddr, handler)
}

func (t *TransportUDP) createConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	laddrStr := laddr.String()
	lc := &net.ListenConfig{}

	protocol := "udp"
	if laddr.IP == nil && raddr.IP.To4() != nil {
		// Use IPV4 if remote is same
		protocol = "udp4"
	}
	addr := raddr.String()

	conn, err := t.pool.addSingleflight(raddr, laddr, t.connectionReuse, func() (Connection, error) {
		udpconn, err := lc.ListenPacket(ctx, protocol, laddrStr)
		if err != nil {
			return nil, err
		}

		c := &UDPConnection{
			PacketConn: udpconn,
			PacketAddr: udpconn.LocalAddr().String(),
			// 1 ref for current return , 2 ref for reader
			refcount: 2 + TransportIdleConnection,
		}
		t.log.Debug("New connection", "raddr", addr)
		go t.readUDPConnection(c, addr, c.PacketAddr, handler)
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	c := conn.(*UDPConnection)
	return c, nil
}

func (t *TransportUDP) readUDPConnection(conn *UDPConnection, raddr string, laddr string, handler MessageHandler) {
	defer t.pool.Delete(raddr) // should be closed in previous defer
	t.readListenerConnection(conn, laddr, handler)
}

func (t *TransportUDP) readListenerConnection(conn *UDPConnection, laddr string, handler MessageHandler) {
	buf := make([]byte, TransportBufferReadSize)
	defer func() {
		if err := t.pool.CloseAndDelete(conn, laddr); err != nil {
			t.log.Warn("connection pool not clean cleanup", "error", err)
		}
	}()
	defer t.log.Debug("Read listener connection stopped", "laddr", laddr)

	var lastRaddr string
	// NOTE: consider to refactor, but for cleanup
	// We are reusing UDP listener as dial connection
	acceptedAddr := make([]string, 0, 1000)
	defer func() {
		t.pool.DeleteMultiple(acceptedAddr)
	}()

	for {
		num, raddr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				t.log.Debug("Read connection closed", "laddr", laddr, "error", err)
				return
			}
			t.log.Error("Read connection error", "laddr", laddr, "error", err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}
		rastr := raddr.String()
		if lastRaddr != rastr {
			// In most cases we are in single connection mode so no need to keep adding in pool
			// In case of server and multiple UDP listeners, this makes sure right one is used
			t.pool.Add(rastr, conn)
			acceptedAddr = append(acceptedAddr, rastr)
		}

		t.parseAndHandle(data, rastr, handler)
		lastRaddr = rastr
	}
}

// This should performe better to avoid any interface allocation
// For now no usage, but leaving here
/* func (t *transportUDP) readUDPConn(conn *net.UDPConn, handler MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer conn.Close()

	for {
		//ReadFromUDP should make one less allocation
		num, raddr, err := conn.ReadFromUDP(buf)

		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				t.log.Debug().Err(err).Msg("Read connection closed")
				return
			}
			t.log.Error().Err(err).Msg("Read UDP connection error")
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr.String(), handler)
	}
} */

func (t *TransportUDP) parseAndHandle(data []byte, src string, handler MessageHandler) {
	// Check is keep alive
	if len(data) <= 4 {
		//One or 2 CRLF
		if len(bytes.Trim(data, "\r\n")) == 0 {
			t.log.Debug("Keep alive CRLF received")
			return
		}
	}

	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error("failed to parse", "data", string(data), "error", err)
		return
	}

	msg.SetTransport(t.Network())
	// Current transaction are taking connection but for UDP they can forward on different src address
	msg.SetSource(src) // By default we expect our source is behind NAT. https://datatracker.ietf.org/doc/html/rfc3581#section-6
	handler(msg)
}

type UDPConnection struct {
	PacketConn net.PacketConn
	PacketAddr string // For faster matching
	Listener   bool

	mu       sync.RWMutex
	refcount int
}

func (c *UDPConnection) close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()

	if c.Listener {
		// In case this UDP created as listener from Serve. Avoid double closing.
		// Closing is done by read connection and it will return already error
		return nil
	}
	DefaultLogger().Debug("UDP reference doing hard close", "ip", c.LocalAddr().String(), "ref", 0)
	return c.PacketConn.Close()
}

func (c *UDPConnection) LocalAddr() net.Addr {
	return c.PacketConn.LocalAddr()
}

func (c *UDPConnection) Ref(i int) int {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	return ref
}

func (c *UDPConnection) Close() error {
	return c.close()
}

func (c *UDPConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()

	if c.Listener {
		// Listeners must be closed manually or by forcing error
		return ref, nil
	}

	DefaultLogger().Debug("UDP reference decrement", "src", c.LocalAddr().String(), "ref", ref)
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		DefaultLogger().Warn("UDP ref went negative on try close", "src", c.LocalAddr().String(), "ref", ref)
		return 0, nil
	}

	return ref, c.close()
}

func (c *UDPConnection) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	// Some debug hook. TODO move to proper way
	n, addr, err = c.PacketConn.ReadFrom(b)
	if SIPDebug && err == nil {
		logSIPRead("UDP", c.PacketConn.LocalAddr().String(), addr.String(), b[:n])
	}
	return n, addr, err
}

func (c *UDPConnection) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.PacketConn.WriteTo(b, addr)
	if SIPDebug && err == nil {
		logSIPWrite("UDP", c.PacketConn.LocalAddr().String(), addr.String(), b[:n])
	}
	return n, err
}

func (c *UDPConnection) WriteMsg(msg Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	if len(data) > UDPMTUSize-200 {
		return ErrUDPMTUCongestion
	}

	var n int
	var err error

	a := msg.remoteAddress() // Destination should be already resolved by transport layer
	if a.IP == nil {
		// Do fallback
		host, port, err := ParseAddr(msg.Destination())
		if err != nil {
			return err
		}
		a.IP = net.ParseIP(host)
		a.Port = port
	}

	raddr := net.UDPAddr{
		IP:   a.IP,
		Port: a.Port,
		Zone: a.Zone,
	}
	if raddr.Port == 0 {
		raddr.Port = DefaultUdpPort
	}

	n, err = c.WriteTo(data, &raddr)
	if err != nil {
		return fmt.Errorf("udp conn %s err. %w", c.PacketConn.LocalAddr().String(), err)
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}
