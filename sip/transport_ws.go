package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// WebSocketProtocols is used in setting websocket header
	// By default clients must accept protocol sip
	WebSocketProtocols = []string{"sip"}
)

// WS transport implementation
type WSTransport struct {
	parser    *Parser
	log       zerolog.Logger
	transport string

	pool   ConnectionPool
	dialer ws.Dialer
}

func NewWSTransport(par *Parser) *WSTransport {
	p := &WSTransport{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: TransportWS,
		dialer:    ws.DefaultDialer,
	}

	p.dialer.Protocols = WebSocketProtocols
	p.log = log.Logger.With().Str("caller", "transport<WS>").Logger()
	return p
}

func (t *WSTransport) String() string {
	return "transport<WS>"
}

func (t *WSTransport) Network() string {
	return t.transport
}

func (t *WSTransport) Close() error {
	t.pool.Clear()
	return nil
}

// Serve is direct way to provide conn on which this worker will listen
func (t *WSTransport) Serve(l net.Listener, handler MessageHandler) error {
	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), l.Addr().String())

	// Prepare handshake header writer from http.Header mapping.
	// Some phones want to return this
	// TODO make this configurable
	header := ws.HandshakeHeaderHTTP(http.Header{
		"Sec-WebSocket-Protocol": WebSocketProtocols,
	})

	u := ws.Upgrader{
		OnBeforeUpgrade: func() (ws.HandshakeHeader, error) {
			return header, nil
		},
	}

	if SIPTrace {
		u.OnHeader = func(key, value []byte) error {
			log.Debug().Str(string(key), string(value)).Msg("non-websocket header:")
			return nil
		}
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Error().Err(err).Msg("Fail to accept conenction")
			return err
		}

		raddr := conn.RemoteAddr().String()

		t.log.Debug().Str("addr", raddr).Msg("New connection accept")

		_, err = u.Upgrade(conn)
		if err != nil {
			t.log.Error().Err(err).Msg("Fail to upgrade")
			continue
		}

		t.initConnection(conn, raddr, false, handler)
	}
}

func (t *WSTransport) initConnection(conn net.Conn, addr string, clientSide bool, handler MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)
	t.log.Debug().Str("raddr", addr).Msg("New WS connection")
	c := &WSConnection{
		Conn:       conn,
		refcount:   1,
		clientSide: clientSide,
	}
	t.pool.Add(addr, c)
	go t.readConnection(c, addr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *WSTransport) readConnection(conn *WSConnection, raddr string, handler MessageHandler) {
	buf := make([]byte, transportBufferSize)
	// defer conn.Close()
	// defer t.pool.Del(raddr)
	defer t.pool.CloseAndDelete(conn, raddr)

	// Create stream parser context
	par := t.parser.NewSIPStream()

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug().Err(err).Msg("Read connection closed")
				return
			}

			t.log.Error().Err(err).Msg("Got TCP error")
			return
		}

		if num == 0 {
			// // What todo
			log.Debug().Msg("Got no bytes, sleeping")
			time.Sleep(100 * time.Millisecond)
			continue
		}

		data := buf[:num]

		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		// Check is keep alive
		if len(data) <= 4 {
			//One or 2 CRLF
			if len(bytes.Trim(data, "\r\n")) == 0 {
				t.log.Debug().Msg("Keep alive CRLF received")
				continue
			}
		}

		t.parseStream(par, data, raddr, handler)
	}

}

// TODO: Try to reuse this from TCP transport as func are same
func (t *WSTransport) parseStream(par *ParserStream, data []byte, src string, handler MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(t.transport)
	msg.SetSource(src)
	handler(msg)
}

// TODO use this when message size limit is defined
func (t *WSTransport) parseFull(data []byte, src string, handler MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(t.transport)
	msg.SetSource(src)
	handler(msg)
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

func (t *WSTransport) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
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

func (t *WSTransport) createConnection(ctx context.Context, laddr *net.TCPAddr, raddr *net.TCPAddr, handler MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	// How to define local interface
	if laddr != nil {
		log.Error().Str("laddr", laddr.String()).Msg("Dialing with local IP is not supported on ws")
	}

	conn, _, _, err := t.dialer.Dial(ctx, "ws://"+addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr, true, handler)
	c.Ref(1)
	return c, nil
}

type WSConnection struct {
	net.Conn

	clientSide bool
	mu         sync.RWMutex
	refcount   int
}

func (c *WSConnection) Ref(i int) int {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", ref).Msg("WS reference increment")
	return ref

}

func (c *WSConnection) Close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", c.refcount).Msg("WS doing hard close")
	return c.Conn.Close()
}

func (c *WSConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", c.refcount).Msg("WS reference decrement")
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		log.Warn().Str("ip", c.RemoteAddr().String()).Int("ref", c.refcount).Msg("WS ref went negative")
		return 0, nil
	}
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", c.refcount).Msg("WS closing")
	return ref, c.Conn.Close()
}

func (c *WSConnection) Read(b []byte) (n int, err error) {
	state := ws.StateServerSide
	if c.clientSide {
		state = ws.StateClientSide
	}
	reader := wsutil.NewReader(c.Conn, state)
	for {
		header, err := reader.NextFrame()
		if err != nil {
			if errors.Is(err, io.EOF) && n > 0 {
				return n, nil
			}
			return n, err
		}

		if SIPTrace {
			log.Debug().Str("caller", c.RemoteAddr().String()).Msgf("WS read connection header <- %s opcode=%d len=%d", c.Conn.RemoteAddr(), header.OpCode, header.Length)
		}

		if header.OpCode == ws.OpClose {
			return n, net.ErrClosed
		}

		data := make([]byte, header.Length)

		// Read until
		_, err = io.ReadFull(c.Conn, data)
		if err != nil {
			return n, err
		}

		// if header.OpCode == ws.OpPing {
		// 	f := ws.NewPongFrame(data)
		// 	ws.WriteFrame(c.Conn, f)
		// 	continue
		// }

		if SIPTrace {
			log.Debug().Msgf("WS read %s <- %s:\n%s", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr(), string(data))
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

	return n, nil
}

func (c *WSConnection) Write(b []byte) (n int, err error) {
	if SIPTrace {
		log.Debug().Str("caller", c.LocalAddr().String()).Msgf("WS write -> %s:\n%s", c.Conn.RemoteAddr(), string(b))
	}

	fs := ws.NewFrame(ws.OpText, true, b)
	if c.clientSide {
		fs = ws.MaskFrameInPlace(fs)
	}
	err = ws.WriteFrame(c.Conn, fs)

	return len(b), err
}

func (c *WSConnection) WriteMsg(msg Message) error {
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
