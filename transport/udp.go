package transport

import (
	"bytes"
	"errors"
	"fmt"
	"net"

	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// UDPReadWorkers defines how many listeners will work
	// Best performance is achieved with low value, to remove high concurency
	UDPReadWorkers int = 1

	UDPMTUSize = 1500

	ErrUDPMTUCongestion = errors.New("size of packet larger than MTU")
)

// UDP transport implementation
type UDPTransport struct {
	// listener *net.UDPConn
	listener net.PacketConn
	parser   sip.Parser
	conn     *UDPConnection

	pool ConnectionPool

	log zerolog.Logger
}

func NewUDPTransport(par sip.Parser) *UDPTransport {
	p := &UDPTransport{
		parser: par,
		conn:   nil, // Making sure interface is nil in returns
		pool:   NewConnectionPool(),
	}
	p.log = log.Logger.With().Str("caller", "transport<UDP>").Logger()
	return p
}

func (t *UDPTransport) String() string {
	return "transport<UDP>"
}

func (t *UDPTransport) Network() string {
	return TransportUDP
}

func (t *UDPTransport) Close() error {
	// return t.connections.Done()
	t.pool.RLock()
	defer t.pool.RUnlock()
	var rerr error
	for _, c := range t.pool.m {
		if _, err := c.TryClose(); err != nil {
			t.log.Err(err).Msg("Fail to close conn")
			rerr = fmt.Errorf("Open connections left")
		}
	}
	return rerr
}

// TODO
// This is more generic way to provide listener and it is blocking
func (t *UDPTransport) ListenAndServe(addr string, handler sip.MessageHandler) error {
	// resolve local UDP endpoint
	laddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("fail to resolve address. err=%w", err)
	}
	udpConn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("listen udp error. err=%w", err)
	}

	return t.Serve(udpConn, handler)
}

// ServeConn is direct way to provide conn on which this worker will listen
// UDPReadWorkers are used to create more workers
func (t *UDPTransport) Serve(conn net.PacketConn, handler sip.MessageHandler) error {

	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), conn.LocalAddr().String())
	/*
		Multiple readers makes problem, which can delay writing response
	*/

	c := &UDPConnection{PacketConn: conn}

	// In case single connection avoid pool
	if t.pool.Size() == 0 {
		t.conn = c
	} else {
		t.conn = nil
	}

	t.pool.Add(conn.LocalAddr().String(), c)

	for i := 0; i < UDPReadWorkers-1; i++ {
		go t.readConnection(c, handler)
	}
	t.readConnection(c, handler)

	return nil
}

func (t *UDPTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveUDPAddr("udp", addr)
}

// GetConnection will return same listener connection
func (t *UDPTransport) GetConnection(addr string) (Connection, error) {
	// Single udp connection as listener can only be used as long IP of a packet in same network
	// In case this is not the case we should return error?
	// https://dadrian.io/blog/posts/udp-in-go/
	if t.conn == nil {
		// Use pool only in multi connection
		return t.pool.Get(addr), nil
	}

	return t.conn, nil
}

// CreateConnection will create new connection. Generally we only
func (t *UDPTransport) CreateConnection(addr string, handler sip.MessageHandler) (Connection, error) {
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	udpconn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, err
	}
	c := &UDPConnection{Conn: udpconn}

	// Wrap it in reference
	t.pool.Add(addr, c)
	go t.readConnection(c, handler)
	return c, err
}

func (t *UDPTransport) readConnection(conn *UDPConnection, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer conn.Close()
	for {
		num, raddr, err := conn.ReadFrom(buf)

		if err != nil {
			t.log.Error().Err(err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr.String(), handler)
	}
}

func (t *UDPTransport) readConnectedConnection(conn *UDPConnection, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer conn.Close()

	raddr := conn.Conn.RemoteAddr().String()
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

		t.parseAndHandle(data, raddr, handler)
	}
}

// This should performe better to avoid any interface allocation
// For now no usage, but leaving here
func (t *UDPTransport) readUDPConn(conn *net.UDPConn, handler sip.MessageHandler) {
	buf := make([]byte, transportBufferSize)
	defer conn.Close()

	for {
		//ReadFromUDP should make one less allocation
		num, raddr, err := conn.ReadFromUDP(buf)

		if err != nil {
			t.log.Error().Err(err)
			return
		}

		data := buf[:num]
		if len(bytes.Trim(data, "\x00")) == 0 {
			continue
		}

		t.parseAndHandle(data, raddr.String(), handler)
	}
}

func (t *UDPTransport) parseAndHandle(data []byte, src string, handler sip.MessageHandler) {
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

	msg.SetTransport(TransportUDP)
	msg.SetSource(src)
	handler(msg)
}

type UDPConnection struct {
	// mutual exclusive for now
	// TODO Refactor
	PacketConn net.PacketConn
	Conn       net.Conn
}

func (c *UDPConnection) Ref(i int) {
	// For now all udp connections must be reused
}

func (c *UDPConnection) Close() error {
	//Do not allow closing UDP
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

func (c *UDPConnection) TryClose() (int, error) {
	return 0, c.Close()
}

func (c *UDPConnection) Read(b []byte) (n int, err error) {
	if SIPDebug {
		log.Debug().Msgf("UDP read %s <- %s:\n%s", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), string(b))
	}
	return c.Conn.Read(b)
}

func (c *UDPConnection) Write(b []byte) (n int, err error) {
	if SIPDebug {
		log.Debug().Msgf("UDP write %s -> %s:\n%s", c.Conn.LocalAddr().String(), c.Conn.RemoteAddr().String(), string(b))
	}
	return c.Conn.Write(b)
}

func (c *UDPConnection) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	// Some debug hook. TODO move to proper way
	n, addr, err = c.PacketConn.ReadFrom(b)
	if SIPDebug {
		log.Debug().Msgf("UDP read %s <- %s:\n%s", c.PacketConn.LocalAddr().String(), addr.String(), string(b))
	}
	return n, addr, err
}

func (c *UDPConnection) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	// Some debug hook. TODO move to proper way
	n, err = c.PacketConn.WriteTo(b, addr)
	if SIPDebug {
		log.Debug().Msgf("UDP write %s -> %s:\n%s", c.PacketConn.LocalAddr().String(), addr.String(), string(b))
	}
	return n, err
}

func (c *UDPConnection) WriteMsg(msg sip.Message) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	if len(data) > UDPMTUSize-200 {
		return ErrUDPMTUCongestion
	}

	var n int
	// TODO doing without if
	if c.Conn != nil {
		var err error
		n, err = c.Write(data)
		if err != nil {
			return fmt.Errorf("conn %s write err=%w", c, err)
		}
	} else {
		var err error

		dst := msg.Destination()
		raddr, err := net.ResolveUDPAddr("udp", dst)
		if err != nil {
			return err
		}

		n, err = c.WriteTo(data, raddr)
		if err != nil {
			return fmt.Errorf("udp conn %s err. %w", c.PacketConn.LocalAddr().String(), err)
		}
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}
