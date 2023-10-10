package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"
	"github.com/quic-go/quic-go"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Quic transport implementation
type QuicTransport struct {
	addr      string
	transport string
	parser    *parser.Parser
	log       zerolog.Logger
	tlsConfig *tls.Config

	listener net.PacketConn

	pool ConnectionPool
}

func NewQuicTransport(par *parser.Parser, dialTlsConfig *tls.Config) *QuicTransport {
	p := &QuicTransport{
		parser:    par,
		pool:      NewConnectionPool(),
		transport: "QUIC",
		tlsConfig: dialTlsConfig,
	}
	p.log = log.Logger.With().Str("caller", "transport<QUIC>").Logger()
	return p
}

func (t *QuicTransport) String() string {
	return "transport<QUIC>"
}

func (t *QuicTransport) Network() string {
	// return "tcp"
	return t.transport
}

func (t *QuicTransport) Close() error {
	// return t.connections.Done()
	t.pool.Clear()
	return nil
}

// Serve is direct way to provide conn on which this worker will listen
func (t *QuicTransport) Serve(ln *quic.Listener, handler sip.MessageHandler) error {
	// t.listener = l
	ctx := context.Background()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if errors.Is(err, quic.ErrServerClosed) {
				err = errors.Join(err, net.ErrClosed) // Be compatible with net
			}
			t.log.Debug().Err(err).Msg("Fail to accept conenction")
			return err
		}
		// conn.CloseWithError()
		// What now if this blocks. Normally this should be next
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			t.log.Error().Err(err).Msg("Failed to get stream")
			continue
		}

		t.initConnection(conn, stream, conn.RemoteAddr().String(), handler)
	}
}

func (t *QuicTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveTCPAddr("tcp", addr)
}

func (t *QuicTransport) GetConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	addr = raddr.String()

	t.log.Debug().Str("addr", addr).Msg("Getting connection")

	c := t.pool.Get(addr)
	return c, nil
}

func (t *QuicTransport) CreateConnection(ctx context.Context, laddr Addr, raddr Addr, handler sip.MessageHandler) (Connection, error) {
	// We are letting transport layer to resolve our address
	// raddr, err := net.ResolveTCPAddr("tcp", addr)
	// if err != nil {
	// 	return nil, err
	// }
	var tladdr *net.UDPAddr = nil
	if laddr.IP != nil {
		tladdr = &net.UDPAddr{
			IP:   laddr.IP,
			Port: laddr.Port,
		}
	}

	traddr := &net.UDPAddr{
		IP:   raddr.IP,
		Port: raddr.Port,
	}
	return t.createConnection(ctx, tladdr, traddr, handler)
}

func (t *QuicTransport) createConnection(ctx context.Context, laddr *net.UDPAddr, raddr *net.UDPAddr, handler sip.MessageHandler) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	udpConn := t.listener
	if t.listener == nil || t.listener.LocalAddr().String() != laddr.String() {
		var err error
		udpConn, err = net.ListenUDP("udp", laddr)
		if err != nil {
			return nil, err
		}
	}

	tr := quic.Transport{
		Conn: udpConn,
	}

	conn, err := tr.Dial(ctx, raddr, t.tlsConfig, &quic.Config{
		// EnableDatagrams: true,
	})
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	c := t.initConnection(conn, stream, addr, handler)

	// Increase ref before returning
	c.Ref(1)
	return c, nil
}

func (t *QuicTransport) initConnection(conn quic.Connection, s quic.Stream, addr string, handler sip.MessageHandler) Connection {
	// // conn.SetKeepAlive(true)
	// conn.SetKeepAlivePeriod(3 * time.Second)

	t.log.Debug().Str("raddr", addr).Msg("New connection")
	c := &QuicConnection{
		Connection: conn,
		s:          s,
		refcount:   1, // Streams should be closed, but underlying connection not
	}
	t.pool.Add(addr, c)
	go t.readConnection(c, addr, handler)
	return c
}

// This should performe better to avoid any interface allocation
func (t *QuicTransport) readConnection(conn *QuicConnection, raddr string, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)

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
		if len(data) <= 4 {
			//One or 2 CRLF
			if len(bytes.Trim(data, "\r\n")) == 0 {
				t.log.Debug().Msg("Keep alive CRLF received")
				continue
			}
		}

		// TODO fallback to parseFull if message size limit is set

		// t.log.Debug().Str("raddr", raddr).Str("data", string(data)).Msg("new message")
		t.parseStream(par, data, raddr, handler)
	}
}

func (t *QuicTransport) parseStream(par *parser.ParserStream, data []byte, src string, handler sip.MessageHandler) {
	msgs, err := par.ParseSIPStream(data)
	if err == parser.ErrParseSipPartial {
		return
	}

	for _, msg := range msgs {
		if err != nil {
			t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
			return
		}

		msg.SetTransport(t.Network())
		msg.SetSource(src)
		handler(msg)
	}
}

// TODO use this when message size limit is defined
func (t *QuicTransport) parseFull(data []byte, src string, handler sip.MessageHandler) {
	msg, err := t.parser.ParseSIP(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(t.Network())
	msg.SetSource(src)
	handler(msg)
}

type QuicConnection struct {
	quic.Connection // underneath connection which can be used for more streams RTP
	s               quic.Stream

	mu       sync.RWMutex
	refcount int
}

func (c *QuicConnection) Ref(i int) int {
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int64("stream", int64(c.s.StreamID())).Int("ref", ref).Msg("QUIC reference increment")
	return ref
}

func (c *QuicConnection) Close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int64("stream", int64(c.s.StreamID())).Int("ref", 0).Msg("QUIC doing hard close")

	// TODO connection can be closed as well.
	return c.s.Close()
}

func (c *QuicConnection) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int64("stream", int64(c.s.StreamID())).Int("ref", ref).Msg("QUIC reference decrement")
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		log.Warn().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int64("stream", int64(c.s.StreamID())).Int("ref", ref).Msg("QUIC ref went negative")
		return 0, nil
	}

	log.Debug().Str("ip", c.LocalAddr().String()).Str("dst", c.RemoteAddr().String()).Int64("stream", int64(c.s.StreamID())).Int("ref", ref).Msg("QUIC closing")
	return ref, c.s.Close()
}

func (c *QuicConnection) Read(b []byte) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.s.Read(b)
	if SIPDebug {
		log.Debug().Msgf("QUIC read %s <- %s:\n%s", c.Connection.LocalAddr().String(), c.Connection.RemoteAddr(), string(b[:n]))
	}
	return n, err
}

func (c *QuicConnection) Write(b []byte) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.s.Write(b)

	if SIPDebug {
		log.Debug().Msgf("QUIC write %s -> %s:\n%s", c.Connection.LocalAddr().String(), c.Connection.RemoteAddr(), string(b[:n]))
	}
	return n, err
}

func (c *QuicConnection) WriteMsg(msg sip.Message) error {
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
