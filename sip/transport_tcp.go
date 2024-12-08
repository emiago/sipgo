package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TCP transport implementation
type transportTCP struct {
	addr      string
	transport string
	parser    *Parser
	log       zerolog.Logger

	pool *ConnectionPool
}

func newTCPTransport(par *Parser) *transportTCP {
	p := &transportTCP{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: TransportTCP,
	}
	p.log = log.Logger.With().Str("caller", "transport<TCP>").Logger()
	return p
}

func (t *transportTCP) String() string {
	return "transport<TCP>"
}

func (t *transportTCP) Network() string {
	// return "tcp"
	return t.transport
}

func (t *transportTCP) Close() error {
	// return t.connections.Done()
	t.pool.Clear()
	return nil
}

// Serve is direct way to provide conn on which this worker will listen
func (t *transportTCP) Serve(l net.Listener, handler MessageHandler) error {
	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), l.Addr().String())
	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Debug().Err(err).Msg("Fail to accept conenction")
			return err
		}

		t.initConnection(conn, conn.RemoteAddr().String(), handler)
	}
}

func (t *transportTCP) GetConnection(addr string) (Connection, error) {
	c := t.pool.Get(addr)
	return c, nil
}

func (t *transportTCP) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
	// We are letting transport layer to resolve our address
	// raddr, err := net.ResolveTCPAddr("tcp", addr)
	// if err != nil {
	// 	return nil, err
	// }
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
	return t.createConnection(ctx, tladdr, traddr, handler)
}

func (t *transportTCP) createConnection(ctx context.Context, laddr *net.TCPAddr, raddr *net.TCPAddr, handler MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	d := net.Dialer{
		LocalAddr: laddr,
	}
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
	c := t.initConnection(conn, addr, handler)

	// Increase ref by 1 before returnin
	c.Ref(1)
	return c, nil
}

func (t *transportTCP) initConnection(conn net.Conn, raddr string, handler MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)
	laddr := conn.LocalAddr().String()
	t.log.Debug().Str("raddr", raddr).Msg("New connection")
	c := &TCPConnection{
		Conn:     conn,
		refcount: 1 + IdleConnection,
	}
	t.pool.Add(laddr, c)
	t.pool.Add(raddr, c)
	go t.readConnection(c, laddr, raddr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *transportTCP) readConnection(conn *TCPConnection, laddr string, raddr string, handler MessageHandler) {
	buf := make([]byte, TransportBufferReadSize)
	defer t.pool.Delete(laddr)
	defer t.pool.CloseAndDelete(conn, raddr)

	// Create stream parser context
	par := t.parser.NewSIPStream()

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug().Err(err).Msg("connection was closed")
				return
			}

			t.log.Error().Err(err).Msg("Read error")
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
				t.log.Debug().Msg("Keep alive CRLF received")
				if datalen == 4 {
					// 2 CRLF is ping
					if _, err := conn.Write(data[:2]); err != nil {
						t.log.Error().Err(err).Msg("Failed to pong keep alive")
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

func (t *transportTCP) parseStream(par *ParserStream, data []byte, src string, handler MessageHandler) {
	err := par.ParseSIPStreamEach(data, func(msg Message) {
		msg.SetTransport(t.Network())
		msg.SetSource(src)
		handler(msg)
	})

	if err != nil {
		if err == ErrParseSipPartial {
			return
		}
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}
}

// TODO use this when message size limit is defined
func (t *transportTCP) parseFull(data []byte, src string, handler MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(t.Network())
	msg.SetSource(src)
	handler(msg)
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
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("TCP reference increment")
	return ref
}

func (c *TCPConnection) Close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", 0).Msg("TCP doing hard close")
	return c.Conn.Close()
}

func (c *TCPConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("TCP reference decrement")
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		log.Warn().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("TCP ref went negative")
		return 0, nil
	}

	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("TCP closing")
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
