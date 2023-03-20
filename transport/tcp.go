package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var ()

// TCP transport implementation
type TCPTransport struct {
	addr      string
	transport string
	listener  net.Listener
	parser    sip.Parser
	handler   sip.MessageHandler
	log       zerolog.Logger

	pool ConnectionPool
}

func NewTCPTransport(addr string, par sip.Parser) *TCPTransport {
	p := &TCPTransport{
		addr:      addr,
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

func (t *TCPTransport) Addr() string {
	return t.addr
}

func (t *TCPTransport) Network() string {
	// return "tcp"
	return t.transport
}

func (t *TCPTransport) Close() error {
	// return t.connections.Done()
	var rerr error
	if t.listener == nil {
		return nil
	}

	if err := t.listener.Close(); err != nil {
		rerr = fmt.Errorf("err=%w", err)
	}

	t.listener = nil
	// t.listenerTCP = nil
	return rerr
}

// TODO
// This is more generic way to provide listener and it is blocking
func (t *TCPTransport) ListenAndServe(handler sip.MessageHandler) error {
	addr := t.addr
	laddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return fmt.Errorf("fail to resolve address. err=%w", err)
	}

	conn, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		return fmt.Errorf("listen tcp error. err=%w", err)
	}

	return t.Serve(conn, handler)
}

// Serve is direct way to provide conn on which this worker will listen
func (t *TCPTransport) Serve(l net.Listener, handler sip.MessageHandler) error {
	if t.listener != nil {
		return fmt.Errorf("TCP transport instance can only listen on one lection")
	}

	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), l.Addr().String())
	t.listener = l
	t.handler = handler
	return t.Accept()
}

func (t *TCPTransport) Accept() error {
	l := t.listener
	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Error().Err(err).Msg("Fail to accept conenction")
			return err
		}

		t.initConnection(conn, conn.RemoteAddr().String())
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

func (t *TCPTransport) CreateConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	return t.createConnection(raddr)
}

func (t *TCPTransport) createConnection(raddr *net.TCPAddr) (Connection, error) {
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

	c := t.initConnection(conn, addr)
	return c, nil
}

func (t *TCPTransport) initConnection(conn net.Conn, addr string) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)

	t.log.Debug().Str("raddr", addr).Msg("New connection")
	c := &TCPConnection{
		Conn:     conn,
		refcount: 1,
	}
	t.pool.Add(addr, c)
	go t.readConnection(c, addr)
	return c
}

// This should performe better to avoid any interface allocation
func (t *TCPTransport) readConnection(conn *TCPConnection, raddr string) {
	buf := make([]byte, UDPbufferSize)

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

		// t.log.Debug().Str("raddr", raddr).Str("data", string(data)).Msg("new message")
		t.parse(data, raddr)
	}
}

func (t *TCPTransport) parse(data []byte, src string) {
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

	msg.SetTransport(t.Network())
	msg.SetSource(src)
	t.handler(msg)
}

type TCPConnection struct {
	net.Conn

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
