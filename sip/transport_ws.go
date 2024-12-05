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
type transportWS struct {
	parser    *Parser
	log       zerolog.Logger
	transport string

	pool   *ConnectionPool
	dialer ws.Dialer
}

func newWSTransport(par *Parser) *transportWS {
	p := &transportWS{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: TransportWS,
		dialer:    ws.DefaultDialer,
	}

	p.dialer.Protocols = WebSocketProtocols
	p.log = log.Logger.With().Str("caller", "transport<WS>").Logger()
	return p
}

func (t *transportWS) String() string {
	return "transport<WS>"
}

func (t *transportWS) Network() string {
	return t.transport
}

func (t *transportWS) Close() error {
	t.pool.Clear()
	return nil
}

// Serve is direct way to provide conn on which this worker will listen
func (t *transportWS) Serve(l net.Listener, handler MessageHandler) error {
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

	if SIPDebug {
		u.OnHeader = func(key, value []byte) error {
			log.Debug().Str(string(key), string(value)).Msg("non-websocket header:")
			return nil
		}
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Error().Err(err).Msg("Failed to accept connection")
			return err
		}

		raddr := conn.RemoteAddr().String()

		t.log.Debug().Str("addr", raddr).Msg("New connection accept")

		_, err = u.Upgrade(conn)
		if err != nil {
			t.log.Error().Err(err).Msg("Fail to upgrade")
			if err := conn.Close(); err != nil {
				t.log.Error().Err(err).Msg("Closing connection failed")
			}
			continue
		}

		t.initConnection(conn, raddr, false, handler)
	}
}

func (t *transportWS) initConnection(conn net.Conn, raddr string, clientSide bool, handler MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)
	laddr := conn.LocalAddr().String()
	t.log.Debug().Str("raddr", raddr).Msg("New WS connection")
	c := &WSConnection{
		Conn:       conn,
		refcount:   1 + IdleConnection,
		clientSide: clientSide,
	}
	t.pool.Add(laddr, c)
	t.pool.Add(raddr, c)
	go t.readConnection(c, laddr, raddr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *transportWS) readConnection(conn *WSConnection, laddr string, raddr string, handler MessageHandler) {
	buf := make([]byte, TransportBufferSize)
	// defer conn.Close()
	// defer t.pool.Del(raddr)
	defer t.pool.Delete(laddr)
	defer t.pool.CloseAndDelete(conn, raddr)
	defer t.log.Debug().Str("raddr", raddr).Msg("Websocket read connection stopped")

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
func (t *transportWS) parseStream(par *ParserStream, data []byte, src string, handler MessageHandler) {
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
func (t *transportWS) parseFull(data []byte, src string, handler MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(t.transport)
	msg.SetSource(src)
	handler(msg)
}

func (t *transportWS) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveTCPAddr("tcp", addr)
}

func (t *transportWS) GetConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	addr = raddr.String()

	c := t.pool.Get(addr)
	return c, nil
}

func (t *transportWS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
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

func (t *transportWS) createConnection(ctx context.Context, laddr *net.TCPAddr, raddr *net.TCPAddr, handler MessageHandler) (Connection, error) {
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
	log.Debug().Str("ip", c.RemoteAddr().String()).Msg("WS doing hard close")
	return c.Conn.Close()
}

func (c *WSConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", ref).Msg("WS reference decrement")
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		log.Warn().Str("ip", c.RemoteAddr().String()).Int("ref", ref).Msg("WS ref went negative")
		return 0, nil
	}
	log.Debug().Str("ip", c.RemoteAddr().String()).Int("ref", ref).Msg("WS closing")
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

		if SIPDebug {
			log.Debug().Str("caller", c.RemoteAddr().String()).Msgf("WS read connection header <- %s opcode=%d len=%d", c.Conn.RemoteAddr(), header.OpCode, header.Length)
		}

		if header.OpCode.IsControl() {
			if header.OpCode == ws.OpClose {
				return n, net.ErrClosed
			}
			continue
		}
		// if header.OpCode.IsReserved() {
		// 	continue
		// }

		// if !header.OpCode.IsData() {
		// 	continue
		// }

		if header.OpCode&ws.OpText == 0 {
			if err := reader.Discard(); err != nil {
				return 0, err
			}
			continue
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

		if header.Masked {
			ws.Cipher(data, header.Mask, 0)
		}

		// header.Masked = false
		if SIPDebug {
			logSIPRead("WS", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), data)
		}

		n += copy(b[n:], data)

		if header.Fin {
			break
		}
	}

	return n, nil
}

func (c *WSConnection) Write(b []byte) (n int, err error) {
	if SIPDebug {
		logSIPWrite("WS", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), b)
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
