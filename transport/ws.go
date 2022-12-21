package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"
	"github.com/gobwas/ws"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var ()

// WS transport implementation
type WSTransport struct {
	addr     string
	listener net.Listener
	parser   parser.SIPParser
	handler  sip.MessageHandler
	log      zerolog.Logger

	pool ConnectionPool
}

func NewWSTransport(addr string, par parser.SIPParser) *WSTransport {
	p := &WSTransport{
		addr:   addr,
		parser: par,
		pool:   NewConnectionPool(),
	}
	p.log = log.Logger.With().Str("caller", "transport<WS>").Logger()
	return p
}

func (t *WSTransport) String() string {
	return "transport<WS>"
}

func (t *WSTransport) Addr() string {
	return t.addr
}

func (t *WSTransport) Network() string {
	return "ws"
}

func (t *WSTransport) Close() error {
	// return t.connections.Done()
	var err error
	if t.listener == nil {
		return nil
	}

	if err := t.listener.Close(); err != nil {
		err = fmt.Errorf("err=%w", err)
	}

	t.listener = nil
	return err
}

// This is more generic way to provide listener and it is blocking
func (t *WSTransport) Serve(handler sip.MessageHandler) error {
	addr := t.addr
	laddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return fmt.Errorf("fail to resolve address. err=%w", err)
	}

	conn, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		return fmt.Errorf("listen tcp error. err=%w", err)
	}

	return t.ServeConn(conn, handler)
}

// serveConn is direct way to provide conn on which this worker will listen
// UDPReadWorkers are used to create more workers
func (t *WSTransport) ServeConn(l net.Listener, handler sip.MessageHandler) error {
	if t.listener != nil {
		return fmt.Errorf("TCP transport instance can only listen on one lection")
	}

	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), l.Addr().String())
	t.listener = l
	t.handler = handler
	return t.Accept()
}

func (t *WSTransport) Accept() error {
	l := t.listener
	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Error().Err(err).Msg("Fail to accept conenction")
			return err
		}

		_, err = ws.Upgrade(conn)
		if err != nil {
			return err
		}

		t.initConnection(conn, conn.RemoteAddr().String())
	}
}

func (t *WSTransport) initConnection(conn net.Conn, addr string) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)
	t.log.Debug().Str("raddr", addr).Msg("New WS connection")
	c := &WSConnection{
		Conn:     conn,
		refcount: 3,
	}
	t.pool.Add(addr, c)
	go t.readConnection(c, addr)
	return c
}

// This should performe better to avoid any interface allocation
func (t *WSTransport) readConnection(conn *WSConnection, raddr string) {
	buf := make([]byte, UDPbufferSize)
	defer conn.Close()
	defer t.pool.Del(raddr)
	defer t.log.Debug().Str("raddr", raddr).Msg("WS connection read stopped")

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.log.Debug().Msg("Got EOF")
				return
			}
			t.log.Error().Err(err).Msg("Got TCP error")
			return
		}

		data := buf[:num]

		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parse(data, raddr)
	}

}

func (t *WSTransport) parse(data []byte, src string) {
	// Check is keep alive
	if len(data) <= 4 {
		//One or 2 CRLF
		if len(bytes.Trim(data, "\r\n")) == 0 {
			t.log.Debug().Msg("Keep alive CRLF received")
			return
		}
	}

	msg, err := t.parser.Parse(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(TransportWS)
	msg.SetSource(src)
	t.handler(msg)
}

func (t *WSTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveTCPAddr("tcp", addr)
}

func (t *WSTransport) GetConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	addr = raddr.String()

	c := t.pool.Get(addr)
	return c, nil
}

func (t *WSTransport) CreateConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	return t.createConnection(raddr.String())
}

func (t *WSTransport) createConnection(addr string) (Connection, error) {
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	conn, _, _, err := ws.Dial(context.TODO(), "ws://"+addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr)
	return c, nil
}

type WSConnection struct {
	net.Conn

	mu       sync.RWMutex
	refcount int
}

func (c *WSConnection) Ref(i int) {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", ref).Msg("WS reference increment")

}

func (c *WSConnection) Close() error {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", c.refcount).Msg("WS reference decrement")
	if ref > 0 {
		return nil
	}
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", c.refcount).Msg("WS closing")
	return c.Conn.Close()
}

func (c *WSConnection) Read(b []byte) (n int, err error) {
	for {
		header, err := ws.ReadHeader(c.Conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}

		if header.OpCode == ws.OpClose {
			return 0, io.EOF
		}

		if SIPDebug {
			log.Debug().Str("caller", c.LocalAddr().String()).Msgf("WS read connection header <- %s len=%d", c.Conn.RemoteAddr(), header.Length)
		}

		data := make([]byte, header.Length)

		// Read until
		_, err = io.ReadFull(c.Conn, data)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}

		if header.Masked {
			ws.Cipher(data, header.Mask, 0)
		}
		// header.Masked = false

		n += copy(b[n:], data)

		if header.Fin {
			break
		}
	}

	if SIPDebug {
		log.Debug().Str("caller", c.LocalAddr().String()).Msgf("WS read connection <- %s: len=%d\n%s", c.Conn.RemoteAddr(), n, string(b))
	}

	return n, nil
}

func (c *WSConnection) Write(b []byte) (n int, err error) {
	fs := ws.NewFrame(ws.OpText, true, b)
	err = ws.WriteFrame(c.Conn, fs)
	if SIPDebug {
		log.Debug().Str("caller", c.LocalAddr().String()).Msgf("WS write -> %s:\n%s", c.Conn.RemoteAddr(), string(b))
	}
	return len(b), err
}

func (c *WSConnection) WriteMsg(msg sip.Message) error {
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
