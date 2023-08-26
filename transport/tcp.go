package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TCP transport implementation
type TCPTransport struct {
	addr      string
	transport string
	parser    sip.Parser
	log       zerolog.Logger

	pool ConnectionPool
}

var (
	HandlePartialMessages bool = true
)

func NewTCPTransport(par sip.Parser) *TCPTransport {
	p := &TCPTransport{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: TransportTCP,
	}
	p.log = log.Logger.With().Str("caller", "transport<TCP>").Logger()
	return p
}

func (t *TCPTransport) String() string {
	return "transport<TCP>"
}

func (t *TCPTransport) Network() string {
	// return "tcp"
	return t.transport
}

func (t *TCPTransport) Close() error {
	// return t.connections.Done()
	// TODO should we empty connection pool

	return nil
}

// Serve is direct way to provide conn on which this worker will listen
func (t *TCPTransport) Serve(l net.Listener, handler sip.MessageHandler) error {
	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), l.Addr().String())
	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Error().Err(err).Msg("Fail to accept conenction")
			return err
		}

		t.initConnection(conn, conn.RemoteAddr().String(), handler)
	}
}

func (t *TCPTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveTCPAddr("tcp", addr)
}

func (t *TCPTransport) GetConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	addr = raddr.String()

	c := t.pool.Get(addr)
	return c, nil
}

func (t *TCPTransport) CreateConnection(addr string, handler sip.MessageHandler) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	return t.createConnection(raddr, handler)
}

func (t *TCPTransport) createConnection(raddr *net.TCPAddr, handler sip.MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	conn, err := net.DialTCP("tcp", nil, raddr)
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
	return c, nil
}

func (t *TCPTransport) initConnection(conn net.Conn, addr string, handler sip.MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)

	t.log.Debug().Str("raddr", addr).Msg("New connection")
	c := &TCPConnection{
		Conn:     conn,
		refcount: 1,
	}
	t.pool.Add(addr, c)
	go t.readConnection(c, addr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *TCPTransport) readConnection(conn *TCPConnection, raddr string, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)

	defer func() {
		// Delete connection from pool only when closed
		ref, _ := conn.TryClose()
		if ref > 0 {
			return
		}
		t.pool.Del(raddr)
	}()
	// defer conn.Close()
	// defer t.pool.Del(raddr)
	defer t.log.Debug().Str("raddr", raddr).Msg("Connection read stopped")

	for {
		num, err := conn.Read(buf)

		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.log.Error().Err(err).Msg("Read error")
			}
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		if len(data) <= 4 {
			//One or 2 CRLF
			if len(bytes.Trim(data, "\r\n")) == 0 {
				t.log.Debug().Msg("Keep alive CRLF received")
				continue
			}
		}

		// t.log.Debug().Str("raddr", raddr).Str("data", string(data)).Msg("new message")
		if HandlePartialMessages {
			messages, err := conn.partialMessageParser.Process(data)
			if err != nil {
				t.log.Error().Err(err).Str("data", string(data)).Msg("failed to extract single message from stream")
			}

			for _, message := range messages {
				t.parseFullMessage(message, raddr, handler)
			}
		} else {
			t.parseFullMessage(data, raddr, handler)
		}
	}
}

func (t *TCPTransport) parseFullMessage(data []byte, src string, handler sip.MessageHandler) {
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
	partialMessageParser parser.PartialMessageParser

	mu       sync.RWMutex
	refcount int
}

func (c *TCPConnection) Ref(i int) {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int("ref", ref).Msg("TCP reference increment")
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
		log.Debug().Msgf("TCP read %s <- %s:\n%s", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr(), string(b))
	}
	return n, err
}

func (c *TCPConnection) Write(b []byte) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.Conn.Write(b)
	if SIPDebug {
		log.Debug().Msgf("TCP write %s -> %s:\n%s", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr(), string(b))
	}
	return n, err
}

func (c *TCPConnection) WriteMsg(msg sip.Message) error {
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
