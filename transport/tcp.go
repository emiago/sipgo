package transport

import (
	"bytes"
	"fmt"
	"net"

	"github.com/emiraganov/sipgo/parser"
	"github.com/emiraganov/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var ()

// TCP transport implementation
type TCPTransport struct {
	listener net.Listener
	parser   parser.SIPParser
	handler  sip.MessageHandler
	log      zerolog.Logger

	pool ConnectionPool
}

func NewTCPTransport(par parser.SIPParser) *TCPTransport {
	p := &TCPTransport{
		parser: par,
		pool:   NewConnectionPool(),
	}
	p.log = log.Logger.With().Str("caller", "transport<TCP>").Logger()
	return p
}

func (t *TCPTransport) String() string {
	return "transport<TCP>"
}

func (t *TCPTransport) Network() string {
	return "tcp"
}

func (t *TCPTransport) Close() error {
	// return t.connections.Done()
	var err error
	if t.listener == nil {
		return nil
	}

	if err := t.listener.Close(); err != nil {
		err = fmt.Errorf("err=%w", err)
	}

	t.listener = nil
	// t.listenerTCP = nil
	return err
}

// TODO
// This is more generic way to provide listener and it is blocking
func (t *TCPTransport) Serve(addr string, handler sip.MessageHandler) error {
	// resolve local UDP endpoint
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
func (t *TCPTransport) ServeConn(l net.Listener, handler sip.MessageHandler) error {
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
		raddr := conn.RemoteAddr().String()
		t.log.Debug().Str("raddr", raddr).Msg("New TCP connection")

		// Add connection to pool
		t.pool.Add(raddr, &Conn{conn})
		go t.readConnection(conn, raddr)
	}
}

func (t *TCPTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveTCPAddr("tcp", addr)
}

func (t *TCPTransport) WriteMsg(msg sip.Message, raddr net.Addr) (err error) {
	rip := raddr.String()
	c, err := t.GetConnection(rip)
	if err != nil {
		return err
	}
	t.log.Debug().Str("raddr", rip).Str("data", msg.StartLine()).Msg("new write")
	err = c.WriteMsg(msg)
	return err
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
	addr = raddr.String()
	return t.createConnection(addr)
}

func (t *TCPTransport) createConnection(addr string) (Connection, error) {
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}
	c := &TCPConnection{conn}
	t.pool.Add(addr, c)
	go t.readConnection(conn, addr)
	return c, nil
}

// This should performe better to avoid any interface allocation
func (t *TCPTransport) readConnection(conn net.Conn, raddr string) {
	buf := make([]byte, UDPbufferSize)
	defer conn.Close()
	defer t.pool.Del(raddr)
	defer t.log.Debug().Str("raddr", raddr).Msg("TCP connection read stopped")

	for {
		num, err := conn.Read(buf)

		if err != nil {
			t.log.Error().Err(err)
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

	msg, err := t.parser.Parse(data) //Very expensive operation
	if err != nil {
		t.log.Error().Err(err).Str("data", string(data)).Msg("failed to parse")
		return
	}

	msg.SetTransport(TransportTCP)
	msg.SetSource(src)
	t.handler(msg)
}

type TCPConnection struct {
	net.Conn
}

func (c *TCPConnection) WriteMsg(msg sip.Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	n, err := c.Write(data)
	if err != nil {
		return fmt.Errorf("conn %s write err=%w", c, err)
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}

type TCPRealConnection struct {
	*net.TCPConn
}

func (c *TCPRealConnection) WriteMsg(msg sip.Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	n, err := c.Write(data)
	if err != nil {
		return fmt.Errorf("conn %s write err=%w", c, err)
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}
