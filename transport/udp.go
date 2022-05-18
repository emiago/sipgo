package transport

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/emiraganov/sipgo/parser"
	"github.com/emiraganov/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// UDPReadWorkers defines how many listeners will work
	// Best performance is achieved with low value, to remove high concurency
	UDPReadWorkers int = 1

	UDPbufferSize uint16 = 65535
)

var strBuilderPool = sync.Pool{
	New: func() interface{} {
		// The Pool's New function should generally only return pointer
		// types, since a pointer can be put into the return interface
		// value without an allocation:
		b := new(strings.Builder)
		// b.Grow(2048)
		return b
	},
}

var bufPool = sync.Pool{
	New: func() interface{} {
		// The Pool's New function should generally only return pointer
		// types, since a pointer can be put into the return interface
		// value without an allocation:
		b := new(bytes.Buffer)
		// b.Grow(2048)
		return b
	},
}

// UDP transport implementation
type UDPTransport struct {
	done chan struct{}
	// listener *net.UDPConn
	listener    net.PacketConn
	listenerUDP *net.UDPConn // TODO consider removing this. There is maybe none benefit if we use if instead interface
	parser      parser.SIPParser
	handler     sip.MessageHandler
	log         zerolog.Logger
}

func NewUDPTransport(par parser.SIPParser) *UDPTransport {
	p := &UDPTransport{
		parser: par,
		done:   make(chan struct{}),
	}
	p.log = log.Logger.With().Str("caller", "transport<UDP>").Logger()
	return p
}

func (t *UDPTransport) String() string {
	return "transport<UDP>"
}

func (t *UDPTransport) Network() string {
	return "udp"
}

func (t *UDPTransport) Close() error {
	// return t.connections.Done()
	var err error
	if t.listener == nil {
		return nil
	}

	if err := t.listener.Close(); err != nil {
		err = fmt.Errorf("%w", err)
	}

	if err := t.listenerUDP.Close(); err != nil {
		err = fmt.Errorf("%w", err)
	}

	t.listener = nil
	t.listenerUDP = nil
	return err
}

// TODO
// This is more generic way to provide listener and it is blocking
func (t *UDPTransport) Serve(addr string, handler sip.MessageHandler) error {
	// resolve local UDP endpoint
	laddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("fail to resolve address. err=%w", err)
	}

	udpConn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("listen udp error. err=%w", err)
	}

	return t.ServeConn(udpConn, handler)
}

// ServeConn is direct way to provide conn on which this worker will listen
// UDPReadWorkers are used to create more workers
func (t *UDPTransport) ServeConn(udpConn net.PacketConn, handler sip.MessageHandler) error {
	if t.listener != nil {
		return fmt.Errorf("UDP transport instance can only listen on one connection")
	}

	t.log.Debug().Msgf("begin listening on %s %s", t.Network(), udpConn.LocalAddr().String())
	t.listener = udpConn
	t.handler = handler
	/*
		Multiple readers makes problem, which can delay writing response
	*/

	switch c := udpConn.(type) {
	case *net.UDPConn:
		t.listenerUDP = c
		// Avoid interface usage, use directly connection
		for i := 0; i < UDPReadWorkers-1; i++ {
			go t.readUDPConn(c)
		}
		t.readUDPConn(c)
	default:
		for i := 0; i < UDPReadWorkers-1; i++ {
			go t.readConnection(udpConn)
		}
		t.readConnection(udpConn)
	}

	return nil
}

func (t *UDPTransport) ResolveAddr(addr string) (net.Addr, error) {
	return net.ResolveUDPAddr("udp", addr)
}

func (t *UDPTransport) WriteMsg(msg sip.Message, raddr net.Addr) error {
	// Using sync pool to remove allocation pressure
	// NOTE: Buffer pool is better than stringer pool. Makes less allocs, avoids conversion
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	msg.StringWrite(buf)

	data := buf.Bytes()
	var err error

	if t.listenerUDP != nil {
		_, err = t.listenerUDP.WriteTo(data, raddr)
	} else {
		_, err = t.listener.WriteTo(data, raddr)
	}

	if err != nil {
		return fmt.Errorf("%s write err=%w", t, err)
	}

	return nil
}

func (t *UDPTransport) readConnection(conn net.PacketConn) {
	buf := make([]byte, UDPbufferSize)
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
