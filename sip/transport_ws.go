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

	// TransportWSKeepAlivePeriod sets how often a Ping frame is sent on an
	// established WS or WSS connection. Proxies and load balancers commonly drop
	// idle SIP over WebSocket connections after 30 to 60 seconds, and without
	// traffic the connection goes stale without either side noticing. Set to 0
	// to disable sending pings.
	TransportWSKeepAlivePeriod = 20 * time.Second
)

// WS transport implementation
type TransportWS struct {
	parser     *Parser
	log        *slog.Logger
	transport  string
	readFilter TransportReadFilter

	connectionReuse bool

	pool   *connectionPool
	dialer ws.Dialer

	DialerCreate func(laddr net.Addr) ws.Dialer

	// DialURI sets default path to use for connecting
	// Experimental
	DialURI func(host string) string

	onConnClose func(conn Connection)
}

func newWSTransport(par *Parser) *TransportWS {
	p := &TransportWS{
		parser:    par,
		pool:      newConnectionPool(),
		transport: "WS",
		dialer:    ws.DefaultDialer,
	}
	return p
}

func (t *TransportWS) init(par *Parser) {
	t.parser = par
	t.pool = newConnectionPool()
	t.transport = "WS"
	t.dialer = ws.DefaultDialer
	t.dialer.Protocols = WebSocketProtocols

	if t.log == nil {
		t.log = DefaultLogger()
	}

	if t.DialerCreate == nil {
		t.DialerCreate = t.dialerCreate
	}

	if t.DialURI == nil {
		t.DialURI = func(addr string) string { return "ws://" + addr }
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
	c := newWSConnection(conn, clientSide, 1+TransportIdleConnection)
	t.pool.Add(laddr, c)
	t.pool.Add(raddr, c)
	go t.readConnection(c, laddr, raddr, handler)
	go c.keepalive(t.log)
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
	defer func() {
		if t.onConnClose != nil {
			t.onConnClose(conn)
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

		if t.readFilter != nil {
			filtered, err := t.readFilter(TransportReadProps{
				Transport:  t.Network(),
				LocalAddr:  conn.LocalAddr(),
				RemoteAddr: conn.RemoteAddr(),
			}, data)
			if err != nil {
				t.log.Error("Read filter error", "laddr", laddr, "raddr", raddr, "error", err)
				return
			}
			if len(filtered) == 0 {
				continue
			}
			data = filtered
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
	msg, err := t.parser.ParseSIP(data) //Very expensive operationParseSIP
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

		dialer := t.DialerCreate(tladdr)
		// How to define local interface
		if tladdr != nil {
			log.Debug("Dialing with local IP is not supported on ws", "laddr", tladdr.String())
		}

		conn, _, _, err := dialer.Dial(ctx, t.DialURI(addr))
		if err != nil {
			return nil, fmt.Errorf("%s dial err=%w", t, err)
		}

		t.log.Debug("New WS connection", "raddr", raddr)
		c := newWSConnection(conn, true, 2+TransportIdleConnection)
		go t.readConnection(c, c.LocalAddr().String(), c.RemoteAddr().String(), handler)
		go c.keepalive(t.log)
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	c := conn.(*WSConnection)
	return c, nil
}

type WSConnection struct {
	net.Conn

	clientSide bool
	mu         sync.RWMutex
	refcount   int

	// writeMu serializes frame writes on Conn. ws frame writing is not safe for
	// concurrent use on a single net.Conn, and pings from the keepalive
	// goroutine and pongs from the read goroutine may now race WriteMsg. Without
	// this two frames can interleave on the wire and neither one decodes.
	writeMu sync.Mutex

	// closeCh is closed once the connection is hard closed, so that the
	// keepalive goroutine stops.
	closeCh   chan struct{}
	closeOnce sync.Once
}

func newWSConnection(conn net.Conn, clientSide bool, refcount int) *WSConnection {
	return &WSConnection{
		Conn:       conn,
		clientSide: clientSide,
		refcount:   refcount,
		closeCh:    make(chan struct{}),
	}
}

// signalClose stops the keepalive goroutine. It is safe to call more than once.
func (c *WSConnection) signalClose() {
	c.closeOnce.Do(func() { close(c.closeCh) })
}

func (c *WSConnection) state() ws.State {
	if c.clientSide {
		return ws.StateClientSide
	}
	return ws.StateServerSide
}

// writeFrame writes a single frame. Every frame written on this connection must
// go through here to keep writes serialized.
func (c *WSConnection) writeFrame(f ws.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return ws.WriteFrame(c.Conn, f)
}

// keepalive sends a Ping frame every TransportWSKeepAlivePeriod until the
// connection is closed. A failed write means the connection is gone, so it is
// closed to wake up the read goroutine and let the pool clean up.
func (c *WSConnection) keepalive(log *slog.Logger) {
	period := TransportWSKeepAlivePeriod
	if period <= 0 {
		return
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			f := ws.NewPingFrame(nil)
			if c.clientSide {
				f = ws.MaskFrameInPlace(f)
			}
			if err := c.writeFrame(f); err != nil {
				log.Debug("WS keepalive ping failed, closing connection", "ip", c.RemoteAddr().String(), "error", err)
				// Close only the underlying conn and leave the refcount alone, so
				// the read goroutine wakes up and the pool runs its normal
				// teardown for this connection.
				if err := c.Conn.Close(); err != nil {
					log.Debug("WS keepalive close failed", "ip", c.RemoteAddr().String(), "error", err)
				}
				return
			}
		}
	}
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
	c.signalClose()
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
	c.signalClose()
	return ref, c.Conn.Close()
}

// handleControlFrame answers a Ping with a Pong and discards a Pong, reading the
// control payload out of reader either way. The write lock is held across the
// whole handler because it may write a header and payload separately, and those
// must not be split by a concurrent Write.
func (c *WSConnection) handleControlFrame(header ws.Header, reader io.Reader, state ws.State) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return wsutil.ControlHandler{
		Src: reader,
		Dst: c.Conn,
		// reader already unmasks the payload it yields.
		DisableSrcCiphering: true,
		State:               state,
	}.Handle(header)
}

func (c *WSConnection) Read(b []byte) (n int, err error) {
	state := c.state()
	reader := wsutil.NewReader(c.Conn, state)
	reader.MaxFrameSize = int64(ParseMaxMessageLength)
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
			DefaultLogger().Debug(str, "caller", c.RemoteAddr().String())
		}

		if header.OpCode.IsControl() {
			if header.OpCode == ws.OpClose {
				return n, net.ErrClosed
			}
			// Ping must be answered with a Pong carrying the same payload, and
			// any control payload must be read out of the stream, otherwise the
			// next header read starts mid payload. See RFC 6455 section 5.5.
			if err := c.handleControlFrame(header, reader, state); err != nil {
				return n, err
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
	err = c.writeFrame(fs)

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
