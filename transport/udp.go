package transport

import (
	"bytes"
	"errors"
	"fmt"
	"net"

	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// UDPReadWorkers defines how many listeners will work
	// Best performance is achieved with low value, to remove high concurency
	UDPReadWorkers int = 1

	UDPbufferSize uint16 = 65535

	UDPMTUSize = 1500

	ErrUDPMTUCongestion = errors.New("size of packet larger than MTU")
)

// UDP transport implementation
type UDPTransport struct {
	// listener *net.UDPConn
	addr     string
	listener net.PacketConn
	parser   parser.SIPParser
	handler  sip.MessageHandler
	conn     *UDPConnection

	log zerolog.Logger
}

func NewUDPTransport(addr string, par parser.SIPParser) *UDPTransport {
	p := &UDPTransport{
		addr:   addr,
		parser: par,
	}
	p.log = log.Logger.With().Str("caller", "transport<UDP>").Logger()
	return p
}

func (t *UDPTransport) String() string {
	return "transport<UDP>"
}

func (t *UDPTransport) Addr() string {
	return t.addr
}

func (t *UDPTransport) Network() string {
	return TransportUDP
}

func (t *UDPTransport) Close() error {
	// return t.connections.Done()
	if t.listener == nil {
		return nil
	}

	var rerr error
	if err := t.listener.Close(); err != nil {
		rerr = err
	}

	t.listener = nil
	t.conn = nil

	return rerr
}

// TODO
// This is more generic way to provide listener and it is blocking
func (t *UDPTransport) ListenAndServe(handler sip.MessageHandler) error {
	// resolve local UDP endpoint
	addr := t.addr
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
	if t.listener != nil {
		return fmt.Errorf("UDP transport instance can only listen on one connection")
	}

	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), conn.LocalAddr().String())
	t.listener = conn

	t.handler = handler

	/*
		Multiple readers makes problem, which can delay writing response
	*/

	t.conn = &UDPConnection{conn}
	for i := 0; i < UDPReadWorkers-1; i++ {
		go t.readConnection(t.conn)
	}
	t.readConnection(t.conn)

	return nil
}

func (t *UDPTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveUDPAddr("udp", addr)
}

// GetConnection will return same listener connection
func (t *UDPTransport) GetConnection(addr string) (Connection, error) {

	return t.conn, nil
}

// CreateConnection will return same listener connection
func (t *UDPTransport) CreateConnection(addr string) (Connection, error) {
	// TODO. Handle connected vs nonconnected udp
	// Normal client can not reuse this connection
	// https://dadrian.io/blog/posts/udp-in-go/
	// raddr, err := net.ResolveUDPAddr("udp", addr)
	// if err != nil {
	// 	return err
	// }

	// conn, err := net.DialUDP("udp", nil, raddr)
	// if err != nil {
	// 	return nil, err
	// }
	// return &UDPConnection{conn}, err

	return t.conn, nil
}

func (t *UDPTransport) readConnection(conn *UDPConnection) {
	buf := make([]byte, UDPbufferSize)
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

		t.parse(data, raddr.String())
	}
}

// This should performe better to avoid any interface allocation
func (t *UDPTransport) readUDPConn(conn *net.UDPConn) {
	buf := make([]byte, UDPbufferSize)
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

		t.parse(data, raddr.String())
	}
}

func (t *UDPTransport) parse(data []byte, src string) {
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

	msg.SetTransport(TransportUDP)
	msg.SetSource(src)
	t.handler(msg)
}

type UDPConnection struct {
	net.PacketConn
}

func (c *UDPConnection) Ref(i int) {
	// For now all udp connections must be reused
}

func (c *UDPConnection) Close() error {
	//Do not allow closing UDP
	return nil
}

func (c *UDPConnection) TryClose() (int, error) {
	return 0, c.Close()
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
	dst := msg.Destination()
	raddr, err := net.ResolveUDPAddr("udp", dst)
	if err != nil {
		return err
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)
	data := buf.Bytes()

	if len(data) > UDPMTUSize-200 {
		return ErrUDPMTUCongestion
	}

	n, err := c.WriteTo(data, raddr)
	if err != nil {
		return fmt.Errorf("udp conn %s err. %w", c.LocalAddr().String(), err)
	}

	if n == 0 {
		return fmt.Errorf("wrote 0 bytes")
	}

	if n != len(data) {
		return fmt.Errorf("fail to write full message")
	}
	return nil
}
