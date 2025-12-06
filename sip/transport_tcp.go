package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// TCP transport implementation
type TransportTCP struct {
	transport       string
	parser          *Parser
	log             *slog.Logger
	connectionReuse bool

	pool *connectionPool

	DialerCreate func(laddr net.Addr) net.Dialer
}

func (t *TransportTCP) init(par *Parser) {
	t.parser = par
	t.pool = newConnectionPool()
	t.transport = "TCP"
	if t.log == nil {
		t.log = DefaultLogger()
	}
	if t.DialerCreate == nil {
		t.DialerCreate = func(laddr net.Addr) net.Dialer {
			return net.Dialer{
				Timeout:   1 * time.Minute,
				LocalAddr: laddr,
			}
		}
	}
}

func (t *TransportTCP) String() string {
	return "Transport<TCP>"
}

func (t *TransportTCP) Network() string {
	// return "tcp"
	return t.transport
}

func (t *TransportTCP) Close() error {
	// return t.connections.Done()
	return t.pool.Clear()
}

// Serve is direct way to provide conn on which this worker will listen
func (t *TransportTCP) Serve(l net.Listener, handler MessageHandler) error {
	t.log.Debug("begin listening on", "network", t.Network(), "laddr", l.Addr().String())
	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Debug("Fail to accept conenction", "error", err)
			return err
		}
		t.initConnection(conn, conn.RemoteAddr().String(), handler)
	}
}

func (t *TransportTCP) GetConnection(addr string) Connection {
	c := t.pool.Get(addr)
	return c
}

func (t *TransportTCP) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	// We are letting transport layer to resolve our address
	// raddr, err := net.ResolveTCPAddr("tcp", addr)
	// if err != nil {
	// 	return nil, err
	// }
	// We do singleflight if laddr is required or connection reuse
	conn, err := t.pool.addSingleflight(raddr, laddr, t.connectionReuse, func() (Connection, error) {
		var tladdr *net.TCPAddr = nil
		if laddr.IP != nil {
			tladdr = &net.TCPAddr{
				IP:   laddr.IP,
				Port: laddr.Port,
			}
		}

		traddr := &net.TCPAddr{
			IP:   raddr.IP,
			Port: raddr.Port,
		}

		addr := traddr.String()
		t.log.Debug("Dialing new connection", "raddr", addr)

		d := t.DialerCreate(tladdr)

		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("%s dial err=%w", t, err)
		}

		// if err := conn.SetKeepAlive(true); err != nil {
		// 	return nil, fmt.Errorf("%s keepalive err=%w", t, err)
		// }

		// if err := conn.SetKeepAlivePeriod(30 * time.Second); err != nil {
		// 	return nil, fmt.Errorf("%s keepalive period err=%w", t, err)
		// }

		t.log.Debug("New connection", "raddr", raddr)
		c := &TCPConnection{
			Conn:     conn,
			refcount: 2 + TransportIdleConnection, // 1 returning + 1 reading + Idle
		}

		go t.readConnection(c, c.LocalAddr().String(), c.RemoteAddr().String(), handler)
		return c, nil
	})
	if err != nil {
		return nil, err
	}

	c := conn.(*TCPConnection)
	return c, nil
}

func (t *TransportTCP) initConnection(conn net.Conn, raddr string, handler MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)
	laddr := conn.LocalAddr().String()
	t.log.Debug("New connection", "raddr", raddr)
	c := &TCPConnection{
		Conn:     conn,
		refcount: 1 + TransportIdleConnection,
	}
	t.pool.Add(laddr, c)
	t.pool.Add(raddr, c)
	go t.readConnection(c, laddr, raddr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *TransportTCP) readConnection(conn *TCPConnection, laddr string, raddr string, handler MessageHandler) {
	buf := make([]byte, TransportBufferReadSize)
	defer t.pool.Delete(laddr)
	defer func() {
		if err := t.pool.CloseAndDelete(conn, raddr); err != nil {
			t.log.Warn("connection pool not clean cleanup", "error", err)
		}
	}()

	// Create stream parser context
	par := t.parser.NewSIPStream()

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug("connection was closed", "error", err)
				return
			}

			t.log.Error("Read error", "error", err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		// Check is keep alive
		datalen := len(data)
		if datalen <= 4 {
			// One or 2 CRLF
			// https://datatracker.ietf.org/doc/html/rfc5626#section-3.5.1
			if len(bytes.Trim(data, "\r\n")) == 0 {
				t.log.Debug("Keep alive CRLF received")
				if datalen == 4 {
					// 2 CRLF is ping
					if _, err := conn.Write(data[:2]); err != nil {
						t.log.Error("Failed to pong keep alive", "error", err)
						return
					}
				}
				continue
			}
		}

		// TODO fallback to parseFull if message size limit is set

		// t.log.Debug().Str("raddr", raddr).Str("data", string(data)).Msg("new message")
		t.parseStream(par, data, raddr, handler)
	}
}

func (t *TransportTCP) parseStream(par *ParserStream, data []byte, src string, handler MessageHandler) {
	err := par.ParseSIPStream(data, func(msg Message) {
		msg.SetTransport(t.Network())
		msg.SetSource(src)
		handler(msg)
	})

	if err != nil {
		if err == ErrParseSipPartial {
			return
		}
		t.log.Error("failed to parse", "error", err, "data", string(data))
		return
	}
}

type TCPConnection struct {
	net.Conn

	mu       sync.RWMutex
	refcount int
}

func (c *TCPConnection) Ref(i int) int {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	DefaultLogger().Debug("TCP reference increment", "ip", c.LocalAddr().String(), "dst", c.RemoteAddr().String(), "ref", ref)
	return ref
}

func (c *TCPConnection) Close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()
	DefaultLogger().Debug("TCP doing hard close", "ip", c.LocalAddr().String(), "dst", c.RemoteAddr().String(), "ref", 0)
	return c.Conn.Close()
}

func (c *TCPConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	DefaultLogger().Debug("TCP reference decrement", "ip", c.LocalAddr().String(), "dst", c.RemoteAddr().String(), "ref", ref)
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		DefaultLogger().Warn("TCP ref went negative", "ip", c.LocalAddr().String(), "dst", c.RemoteAddr().String(), "ref", ref)
		return 0, nil
	}

	DefaultLogger().Debug("TCP closing", "ip", c.LocalAddr().String(), "dst", c.RemoteAddr().String(), "ref", ref)
	return ref, c.Conn.Close()
}

func (c *TCPConnection) Read(b []byte) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.Conn.Read(b)
	if SIPDebug {
		logSIPRead("TCP", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), b[:n])
	}
	return n, err
}

func (c *TCPConnection) Write(b []byte) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.Conn.Write(b)
	if SIPDebug {
		logSIPWrite("TCP", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), b[:n])
	}
	return n, err
}

func (c *TCPConnection) WriteMsg(msg Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	n, err := c.Write(data)
	if err != nil {
		return fmt.Errorf("conn %s write err=%w", c.RemoteAddr().String(), err)
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}
