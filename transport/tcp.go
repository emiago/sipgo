package transport

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	sipgo "github.com/emiago/sipgo/sip"

	"github.com/livekit/sipgo/sip"
)

type PortRange struct {
	Min, Max int
}

type TCPConfig struct {
	DialPorts PortRange
}

// TCP transport implementation
type TCPTransport struct {
	c         TCPConfig
	addr      string
	transport string
	parser    *sipgo.Parser
	log       *slog.Logger

	pool *ConnectionPool
}

func NewTCPTransport(log *slog.Logger, par *sipgo.Parser, c *TCPConfig) *TCPTransport {
	p := &TCPTransport{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: TransportTCP,
	}
	if c != nil {
		p.c = *c
	}
	p.log = log.With("caller", "transport<TCP>")
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
	t.pool.Clear(t.log)
	return nil
}

// Serve is direct way to provide conn on which this worker will listen
func (t *TCPTransport) Serve(l net.Listener, handler sip.MessageHandler) error {
	t.log.Debug("begin listening on", "net", t.Network(), "addr", l.Addr())
	for {
		conn, err := l.Accept()
		if err != nil {
			t.log.Debug("Fail to accept conenction", "err", err)
			return err
		}

		t.initConnection(conn, conn.RemoteAddr().String(), 1, handler)
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

	t.log.Debug("Getting connection", "addr", addr)

	c := t.pool.Get(addr)
	return c, nil
}

func (t *TCPTransport) CreateConnection(laddr Addr, host string, raddr Addr, handler sip.MessageHandler) (Connection, error) {
	// We are letting transport layer to resolve our address
	// raddr, err := net.ResolveTCPAddr("tcp", addr)
	// if err != nil {
	// 	return nil, err
	// }

	traddr := &net.TCPAddr{
		IP:   raddr.IP,
		Port: raddr.Port,
	}
	return t.createConnection(traddr, handler)
}

func (t *TCPTransport) createConnection(raddr *net.TCPAddr, handler sip.MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug("Dialing new connection", "raddr", addr)

	conn, err := bindRange(t.c.DialPorts.Min, t.c.DialPorts.Max, func(lport int) (*net.TCPConn, error) {
		return net.DialTCP("tcp", &net.TCPAddr{
			IP:   nil, // let os decide
			Port: lport,
		}, raddr)
	})
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	// if err := conn.SetKeepAlive(true); err != nil {
	// 	return nil, fmt.Errorf("%s keepalive err=%w", t, err)
	// }

	// if err := conn.SetKeepAlivePeriod(30 * time.Second); err != nil {
	// 	return nil, fmt.Errorf("%s keepalive period err=%w", t, err)
	// }

	c := t.initConnection(conn, addr, 2, handler)
	return c, nil
}

// initConnection pools conn with refs references: 1 for the pool, plus 1 for
// the caller when it dialed.
func (t *TCPTransport) initConnection(conn net.Conn, addr string, refs int, handler sip.MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)

	t.log.Debug("New connection", "raddr", addr)
	c := &TCPConnection{
		Conn:     conn,
		log:      t.log,
		refcount: refs,
	}
	c.drop = func() { t.pool.CloseAndDelete(t.log, c, addr) }
	t.pool.Add(addr, c)
	go t.readConnection(c, addr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *TCPTransport) readConnection(conn *TCPConnection, raddr string, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)

	connectionOpened.WithLabelValues(t.Network()).Inc()
	connectionsOpen.WithLabelValues(t.Network()).Inc()

	// reason is set before every return, so the close counter and the gauge
	// cannot drift apart from each other or from the pool.
	reason := "unknown"
	defer func() {
		connectionsOpen.WithLabelValues(t.Network()).Dec()
		connectionClosed.WithLabelValues(t.Network(), reason).Inc()
		conn.dropFromPool()
	}()

	// Create stream parser context
	par := t.parser.NewSIPStream()
	defer par.Close()

	// partial is true while a message is still being assembled. The parser
	// cannot be asked: it drains complete header lines as it parses them, so
	// its buffer stays near empty however far along the message is.
	partial := false
	// staleResets counts read deadlines that found a message incomplete, with
	// nothing framed in between.
	staleResets := 0
	// everFramed stays false until the peer sends one complete SIP message.
	everFramed := false

	for {
		num, err := conn.Read(buf)
		if err != nil {
			if conn.writeFailed.Load() {
				t.log.Warn("closed connection after a failed write", "raddr", raddr)
				reason = "write_error"
				return
			}

			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				t.log.Debug("connection was closed", "err", err)
				reason = "peer_close"
				return
			}

			if errors.Is(err, os.ErrDeadlineExceeded) {
				// A failed handshake makes every later Read return the same
				// error at once, so carrying on would spin.
				if tc, ok := conn.Conn.(*tls.Conn); ok && !tc.ConnectionState().HandshakeComplete {
					t.log.Info("closing connection, tls handshake did not complete", "raddr", raddr)
					reason = "tls_handshake_did_not_complete"
					return
				}

				// Nothing arrived for a whole TCPReadTimeout. An idle connection
				// is left open: some peers dial once, cache the connection and
				// never rebuild it, so closing one can cost us every later
				// request from that peer.
				if !partial {
					continue
				}

				// A message was left incomplete, so the rest of it is not
				// coming. Twice with nothing framed in between means the peer
				// keeps abandoning messages part way and the connection is not
				// going to recover.
				staleResets++
				if staleResets > 1 {
					if everFramed {
						t.log.Error("closing connection, peer abandoned a second incomplete sip message",
							"raddr", raddr)
						reason = "stale_partial_repeated"
					} else {
						reason = "stale_partial_repeated_but_never_framed"
					}
					return
				}

				// Drop it, otherwise the next bytes to arrive are appended to a
				// fragment that can never complete and are lost with it.
				t.log.Info("discarding incomplete sip message, nothing followed it",
					"raddr", raddr)
				par.Reset()
				partial = false
				parserReset.WithLabelValues("tcp", "stale_partial").Inc()
				continue
			}

			t.log.Debug("Read error", "err", err)
			reason = "read_error"
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		// Check is keep alive
		if len(data) <= 4 {
			//One or 2 CRLF
			if len(bytes.Trim(data, "\r\n")) == 0 {
				t.log.Debug("Keep alive CRLF received")
				continue
			}
		}

		// TODO fallback to parseFull if message size limit is set

		framed, err := t.parseStream(par, data, raddr, handler)
		if framed > 0 {
			// The stream is framing messages again, so an earlier abandoned one
			// was a one off rather than a pattern.
			staleResets = 0
			everFramed = true
		}

		// A partial message is normal on a stream transport. Anything else means
		// the parser cannot frame this stream any more, and there is no reliable
		// way to find the next message boundary in a byte stream, so the only way
		// back to a known one is a new connection. Reads keep succeeding either
		// way, so nothing else would notice.
		//
		// The parser bounds an incomplete message itself and reports
		// ErrMessageTooLarge, but only from the path that knows the message is
		// incomplete. A CR with no LF takes a different path, consumes nothing and
		// returns the same error on every later read, so it has to be caught here.
		partial = errors.Is(err, sipgo.ErrParseSipPartial)
		if err != nil && !partial {
			// A peer that framed a message and then stopped framing is a
			// defect worth an error. A peer that never framed one is probably
			// just a port scanner.
			if everFramed {
				reason = closeReason(err)
				stuck := par.Buffer().Bytes()
				t.log.Error("closing connection, sip stream cannot be framed",
					"err", err, "reason", reason, "raddr", raddr, "unparsed", len(stuck))
				// The bytes say why, but they are peer traffic, so they stay out
				// of the error above and are capped.
				t.log.Debug("unparsed sip stream", "raddr", raddr, "data", dataSample(stuck))
			} else {
				reason = "never_framed"
			}
			return
		}
	}
}

// parseStream feeds one read to the parser and reports how many messages it
// framed, so the caller can tell a stream that is making progress from one that
// is not.
func (t *TCPTransport) parseStream(par *sipgo.ParserStream, data []byte, src string, handler sip.MessageHandler) (int, error) {
	bytesPacketSize.WithLabelValues("tcp", "read").Observe(float64(len(data)))
	framed := 0
	err := par.ParseSIPStream(data, func(msg sipgo.Message) {
		framed++
		msg.SetTransport(t.Network())
		msg.SetSource(src)
		handler(msg)
	})
	return framed, err
}

// dataSample caps peer data so a log line cannot carry a whole read buffer.
func dataSample(b []byte) string {
	const max = 256
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// closeReason maps a parse failure to a metric label.
func closeReason(err error) string {
	switch {
	case errors.Is(err, sipgo.ErrMessageTooLarge):
		return "message_too_large"
	case errors.Is(err, sipgo.ErrParseLineNoCRLF):
		return "no_crlf"
	default:
		return "parse_error"
	}
}

// TODO use this when message size limit is defined
func (t *TCPTransport) parseFull(data []byte, src string, handler sip.MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Info("failed to parse", "err", err, "data", string(data))
		return
	}

	msg.SetTransport(t.Network())
	msg.SetSource(src)
	handler(msg)
}

type TCPConnection struct {
	net.Conn
	log *slog.Logger

	// drop takes the connection out of its pool and closes it.
	drop     func()
	dropOnce sync.Once
	// writeFailed tells the read loop why the connection closed under it.
	writeFailed atomic.Bool

	mu       sync.RWMutex
	refcount int
	// closed is set once the conn is closed. Holders may still release their
	// references afterwards, so refcount is left alone.
	closed bool
}

// dropFromPool runs drop once, whichever of the read loop and a failed write
// gets there first. Removal is done before returning, so a caller that redials
// after a failed write cannot be handed this connection again.
func (c *TCPConnection) dropFromPool() {
	if c.drop != nil {
		c.dropOnce.Do(c.drop)
	}
}

func (c *TCPConnection) Ref(i int) int {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	c.log.Debug("TCP reference increment", "ip", c.LocalAddr(), "dst", c.RemoteAddr(), "ref", ref)
	return ref
}

func (c *TCPConnection) Close() error {
	c.mu.Lock()
	closed := c.closed
	c.closed = true
	ref := c.refcount
	c.mu.Unlock()
	if closed {
		return nil
	}
	c.log.Debug("TCP doing hard close", "ip", c.LocalAddr(), "dst", c.RemoteAddr(), "ref", ref)
	return c.Conn.Close()
}

func (c *TCPConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref, closed := c.refcount, c.closed
	if ref == 0 {
		c.closed = true
	}
	c.mu.Unlock()
	c.log.Debug("TCP reference decrement", "ip", c.LocalAddr(), "dst", c.RemoteAddr(), "ref", ref)
	if ref < 0 {
		c.log.Warn("TCP ref went negative", "ip", c.LocalAddr(), "dst", c.RemoteAddr(), "ref", ref)
		return 0, nil
	}
	if ref > 0 {
		return ref, nil
	}
	if closed {
		return 0, nil
	}
	c.log.Debug("TCP closing", "ip", c.LocalAddr(), "dst", c.RemoteAddr(), "ref", ref)
	return 0, c.Conn.Close()
}

func (c *TCPConnection) Read(b []byte) (n int, err error) {
	if TCPReadTimeout > 0 {
		if err := c.Conn.SetReadDeadline(time.Now().Add(TCPReadTimeout)); err != nil {
			return 0, err
		}
	}

	// Some debug hook. TODO move to proper way
	n, err = c.Conn.Read(b)
	if SIPDebug {
		c.log.Debug("TCP read", "local", c.Conn.LocalAddr(), "remote", c.Conn.RemoteAddr(), "data", string(b[:n]))
	}
	return n, err
}

func (c *TCPConnection) Write(b []byte) (n int, err error) {
	// Some debug hook. TODO move to proper way
	if TCPWriteTimeout > 0 {
		if err := c.Conn.SetWriteDeadline(time.Now().Add(TCPWriteTimeout)); err != nil {
			return 0, err
		}
	}
	n, err = c.Conn.Write(b)
	if SIPDebug {
		c.log.Debug("TCP write", "local", c.Conn.LocalAddr(), "remote", c.Conn.RemoteAddr(), "data", string(b[:n]))
	}
	return n, err
}

func (c *TCPConnection) WriteMsg(msg sip.Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	bytesPacketSize.WithLabelValues("tcp", "write").Observe(float64(len(data)))

	n, err := c.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// Part of the message may be on the wire, so the stream can no longer
		// be framed, and crypto/tls fails every write after a failed one.
		c.writeFailed.Store(true)
		c.dropFromPool()
		return fmt.Errorf("conn %s write err=%w", c.RemoteAddr().String(), err)
	}
	return nil
}

type bindFunc[T any] func(port int) (T, error)

var ErrCannotBindPort = errors.New("cannot allocate port")

func bindRange[T any](portMin, portMax int, create bindFunc[T]) (T, error) {
	var zero T
	if portMin <= 0 && portMax <= 0 {
		return create(0)
	} else if portMin == portMax {
		return create(portMin)
	}
	if portMin <= 0 {
		portMin = 1
	}
	if portMax <= 0 || portMax > 0xFFFF {
		portMax = 0xFFFF
	}
	if portMin > portMax {
		return zero, errors.New("invalid range")
	}

	ports := portMax - portMin + 1
	portCurrent := rand.Intn(ports) + portMin

	for try := 0; try < ports; try++ {
		c, err := create(portCurrent)
		if err == nil {
			return c, nil
		} else if !errors.Is(err, syscall.EADDRINUSE) {
			return c, err
		}
		portCurrent++
		if portCurrent > portMax {
			portCurrent = portMin
		}
	}
	return zero, ErrCannotBindPort
}
