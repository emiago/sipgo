package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

var (
	// WebSocketProtocols is used in setting websocket header
	// By default clients must accept protocol sip
	WebSocketProtocols = []string{"sip"}
)

// WS transport implementation
type TransportWS struct {
	parser    *Parser
	log       *slog.Logger
	transport string

	connectionReuse bool

	pool   *ConnectionPool
	dialer ws.Dialer

	DialerCreate func(laddr net.Addr) ws.Dialer
}

func newWSTransport(par *Parser) *TransportWS {
	p := &TransportWS{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: "WS",
		dialer:    ws.DefaultDialer,
	}
	p.dialer.Protocols = WebSocketProtocols
	// p.log = log.Logger.With().Str("caller", "transport<WS>").Logger()
	return p
}

func (t *TransportWS) init(par *Parser) {
	t.parser = par
	t.pool = NewConnectionPool()
	t.transport = "WS"
	t.dialer = ws.DefaultDialer
	t.dialer.Protocols = WebSocketProtocols

	if t.log == nil {
		t.log = slog.Default()
	}

	if t.DialerCreate == nil {
		t.DialerCreate = t.dialerCreate
	}
}

func (t *TransportWS) dialerCreate(laddr net.Addr) ws.Dialer {
	if laddr == nil {
		return t.dialer
	}

	netDialer := net.Dialer{
		LocalAddr: laddr,
	}

	dialer := ws.Dialer{
		NetDial: netDialer.DialContext,
	}
	dialer.Protocols = WebSocketProtocols
	return dialer
}

func (t *TransportWS) String() string {
	return "transport<WS>"
}

func (t *TransportWS) Network() string {
	return t.transport
}

func (t *TransportWS) Close() error {
	return t.pool.Clear()
}

// Serve is direct way to provide conn on which this worker will listen
func (t *TransportWS) Serve(l net.Listener, handler MessageHandler) error {
	log := t.log
	log.Debug("begin listening on", "network", t.Network(), "laddr", l.Addr().String())

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
			log.Debug("non-websocket header:", string(key), string(value))
			return nil
		}
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Error("Failed to accept connection", "error", err)
			}
			return err
		}

		raddr := conn.RemoteAddr().String()

		log.Debug("New connection accept", "addr", raddr)

		_, err = u.Upgrade(conn)
		if err != nil {
			log.Error("Fail to upgrade", "error", err)
			if err := conn.Close(); err != nil {
				log.Error("Closing connection failed", "error", err)
			}
			continue
		}

		t.initConnection(conn, raddr, false, handler)
	}
}

func (t *TransportWS) initConnection(conn net.Conn, raddr string, clientSide bool, handler MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)
	laddr := conn.LocalAddr().String()
	t.log.Debug("New WS connection", "raddr", raddr)
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
func (t *TransportWS) readConnection(conn *WSConnection, laddr string, raddr string, handler MessageHandler) {
	log := t.log
	buf := make([]byte, TransportBufferReadSize)
	// defer conn.Close()
	// defer t.pool.Del(raddr)
	defer t.pool.Delete(laddr)
	defer func() {
		if err := t.pool.CloseAndDelete(conn, raddr); err != nil {
			t.log.Warn("connection pool not clean cleanup", "error", err)
		}
	}()
	defer log.Debug("Websocket read connection stopped", "raddr", raddr)

	// Create stream parser context
	par := t.parser.NewSIPStream()

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug("Read connection closed", "error", err)
				return
			}

			t.log.Error("Got TCP error", "error", err)
			return
		}

		if num == 0 {
			// // What todo
			log.Debug("Got no bytes, sleeping")
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
				log.Debug("Keep alive CRLF received")
				continue
			}
		}

		t.parseStream(par, data, raddr, handler)
	}

}

// TODO: Try to reuse this from TCP transport as func are same
func (t *TransportWS) parseStream(par *ParserStream, data []byte, src string, handler MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error("failed to parse", "error", err, "data", string(data))
		return
	}

	msg.SetTransport(t.transport)
	msg.SetSource(src)
	handler(msg)
}

func (t *TransportWS) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveTCPAddr("tcp", addr)
}

func (t *TransportWS) GetConnection(addr string) Connection {
	return t.pool.Get(addr)
}

func (t *TransportWS) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler MessageHandler) (Connection, error) {
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

		log := t.log
		addr := traddr.String()
		log.Debug("Dialing new connection", "raddr", addr)

		dialer := t.dialerCreate(tladdr)
		// How to define local interface
		if tladdr != nil {
			log.Debug("Dialing with local IP is not supported on ws", "laddr", tladdr.String())
		}

		conn, _, _, err := dialer.Dial(ctx, "ws://"+addr)
		if err != nil {
			return nil, fmt.Errorf("%s dial err=%w", t, err)
		}

		t.log.Debug("New WS connection", "raddr", raddr)
		c := &WSConnection{
			Conn:       conn,
			refcount:   2 + IdleConnection,
			clientSide: true,
		}
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	c := conn.(*WSConnection)
	go t.readConnection(c, c.LocalAddr().String(), c.RemoteAddr().String(), handler)
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
	DefaultLogger().Debug("WS reference increment", "ip", c.RemoteAddr().String(), "ref", ref)
	return ref

}

func (c *WSConnection) Close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()
	DefaultLogger().Debug("WS doing hard close", "ip", c.RemoteAddr().String())
	return c.Conn.Close()
}

func (c *WSConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	DefaultLogger().Debug("WS reference decrement", "ip", c.RemoteAddr().String(), "ref", ref)
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		DefaultLogger().Warn("WS ref went negative", "ip", c.RemoteAddr().String(), "ref", ref)
		return 0, nil
	}
	DefaultLogger().Debug("WS closing", "ip", c.RemoteAddr().String(), "ref", ref)
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
			str := fmt.Sprintf("WS read connection header <- %s opcode=%d len=%d", c.Conn.RemoteAddr(), header.OpCode, header.Length)
			slog.Debug(str, "caller", c.RemoteAddr().String())
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
