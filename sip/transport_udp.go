package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	UDPMTUSize = 1500

	// UDPUseConnectedConnection will force creating UDP connected connection
	// UDPUseConnectedConnection = false

	ErrUDPMTUCongestion = errors.New("size of packet larger than MTU")
)

// UDP transport implementation
type transportUDP struct {
	// listener *net.UDPConn
	parser *Parser

	pool *ConnectionPool
	log  zerolog.Logger
}

func newUDPTransport(par *Parser) *transportUDP {
	p := &transportUDP{
		parser: par,
		pool:   NewConnectionPool(),
	}
	p.log = log.Logger.With().Str("caller", "transport<UDP>").Logger()
	return p
}

func (t *transportUDP) String() string {
	return "transport<UDP>"
}

func (t *transportUDP) Network() string {
	return TransportUDP
}

func (t *transportUDP) Close() error {
	t.pool.Clear()
	// Closing listeners is caller thing.
	return nil
}

// ServeConn is direct way to provide conn on which this worker will listen
func (t *transportUDP) Serve(conn net.PacketConn, handler MessageHandler) error {

	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), conn.LocalAddr().String())
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

func (t *transportUDP) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveUDPAddr("udp", addr)
}

// GetConnection will return same listener connection
func (t *transportUDP) GetConnection(addr string) (Connection, error) {
	// Single udp connection as listener can only be used as long IP of a packet in same network
	// In case this is not the case we should return error?
	// https://dadrian.io/blog/posts/udp-in-go/
	// Pool consists either of every new packet From addr or client created connection
	if conn := t.pool.Get(addr); conn != nil {
		return conn, nil
	}

	return nil, nil
}

// CreateConnection will create new connection
func (t *transportUDP) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	// if UDPUseConnectedConnection {
	// 	return t.createConnectedConnection(ctx, laddr, raddr, handler)
	// }
	return t.createConnection(ctx, laddr, raddr, handler)
}

func (t *transportUDP) createConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	laddrStr := laddr.String()
	lc := &net.ListenConfig{}
	udpconn, err := lc.ListenPacket(ctx, "udp", laddrStr)
	if err != nil {
		return nil, err
	}

	c := &UDPConnection{
		PacketConn: udpconn,
		PacketAddr: udpconn.LocalAddr().String(),
		// 1 ref for current return , 2 ref for reader
		refcount: 2 + IdleConnection,
	}

	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("New connection")

	// Add in pool but as listen connection
	// Reason is that UDP connection can be reused.
	// Notice this can only be reused if VIA header is set explicitly like WithClientAddr()
	t.pool.Add(c.PacketAddr, c)

	// We need to also have mapping remote add to this connection
	t.pool.Add(addr, c)
	// t.listeners = append(t.listeners, c)
	go t.readUDPConnection(c, addr, c.PacketAddr, handler)
	return c, err
}

func (t *transportUDP) readUDPConnection(conn *UDPConnection, raddr string, laddr string, handler MessageHandler) {
	defer t.pool.Delete(raddr) // should be closed in previous defer
	t.readListenerConnection(conn, laddr, handler)
}

// The major problem here is in case you are creating connected connection on non unicast (0.0.0.0)
// via unicast 127.0.0.1
// This GO will fail to read as it is getting responses from 0.0.0.0
// More bigger problem are responses that are ariving from different IP ranges
// ex
// 192.168.... -> 127.0.0.1
// 192.168..... <- 192.168..  This will not work as connected connection can not handle this
/* func (t *transportUDP) createConnectedConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	var uladdr *net.UDPAddr = nil
	if laddr.IP != nil {
		uladdr = &net.UDPAddr{
			IP:   laddr.IP,
			Port: laddr.Port,
		}
	}

	d := net.Dialer{
		LocalAddr: uladdr,
	}

	addr := raddr.String()
	udpconn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}

	c := &UDPConnection{
		Conn: udpconn,
		// 1 ref for current return , 2 ref for reader
		refcount: 2 + IdleConnection,
	}

	t.log.Debug().Str("raddr", addr).Msg("New connected connection")

	// Wrap it in reference
	t.pool.Add(addr, c)
	go t.readConnectedConnection(c, handler)

	return c, err
} */

func (t *transportUDP) readListenerConnection(conn *UDPConnection, laddr string, handler MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer t.pool.CloseAndDelete(conn, laddr)
	defer t.log.Debug().Str("addr", laddr).Msg("Read listener connection stopped")

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
				t.log.Debug().Str("addr", laddr).Err(err).Msg("Read connection closed")
				return
			}
			t.log.Error().Str("addr", laddr).Err(err).Msg("Read connection error")
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

/* func (t *transportUDP) readConnectedConnection(conn *UDPConnection, handler MessageHandler) {
	buf := make([]byte, transportBufferSize)
	raddr := conn.Conn.RemoteAddr().String()
	defer t.pool.CloseAndDelete(conn, raddr)
	defer t.log.Debug().Str("raddr", raddr).Msg("Read connected connection stopped")

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug().Err(err).Msg("Read connection closed")
				return
			}
			t.log.Error().Err(err).Msg("Read connection error")
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr, handler)
	}
} */

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

func (t *transportUDP) parseAndHandle(data []byte, src string, handler MessageHandler) {
	// Check is keep alive
	if len(data) <= 4 {
		//One or 2 CRLF
		if len(bytes.Trim(data, "\r\n")) == 0 {
			t.log.Debug().Msg("Keep alive CRLF received")
			return
		}
	}

	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(TransportUDP)
	// TODO should we avoid this and let source be inspected.
	// Current transaction are taking connection but for UDP they can forward on different src address
	msg.SetSource(src) // By default we expect our source is behind NAT. https://datatracker.ietf.org/doc/html/rfc3581#section-6
	handler(msg)
}

type UDPConnection struct {
	// mutual exclusive for now to avoid interface for Read
	// TODO Refactor
	PacketConn net.PacketConn
	PacketAddr string // For faster matching
	Listener   bool

	Conn net.Conn

	mu       sync.RWMutex
	refcount int
}

func (c *UDPConnection) close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()

	if c.Conn != nil {
		log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.Conn.RemoteAddr().String()).Int("ref", 0).Msg("UDP doing hard close")
		return c.Conn.Close()
	}

	if c.Listener {
		// In case this UDP created as listener from Serve. Avoid double closing.
		// Closing is done by read connection and it will return already error
		return nil
	}
	log.Debug().Str("ip", c.LocalAddr().String()).Int("ref", 0).Msg("UDP listener doing hard close")
	return c.PacketConn.Close()
}

func (c *UDPConnection) LocalAddr() net.Addr {
	if c.Conn != nil {
		return c.Conn.LocalAddr()
	}
	return c.PacketConn.LocalAddr()
}

func (c *UDPConnection) RemoteAddr() net.Addr {
	if c.Conn != nil {
		return c.Conn.RemoteAddr()
	}
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

	log.Debug().Str("src", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("UDP reference decrement")
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		log.Warn().Str("src", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("UDP ref went negative")
		return 0, nil
	}

	return ref, c.close()
}

func (c *UDPConnection) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if SIPDebug {
		logSIPRead("UDP", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), b[:n])
	}
	return n, err
}

func (c *UDPConnection) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if SIPDebug {
		logSIPWrite("UDP", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), b[:n])
	}
	return n, err
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
	// TODO doing without if
	if c.Conn != nil {
		var err error
		n, err = c.Write(data)
		if err != nil {
			return fmt.Errorf("conn %s write err=%w", c.Conn.LocalAddr().String(), err)
		}
	} else {
		var err error

		// TODO lets return this better
		dst := msg.Destination() // Destination should be already resolved by transport layer
		host, port, err := ParseAddr(dst)
		if err != nil {
			return err
		}
		raddr := net.UDPAddr{
			IP:   net.ParseIP(host),
			Port: port,
		}

		if raddr.Port == 0 {
			raddr.Port = DefaultUdpPort
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
