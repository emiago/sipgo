package transport

import (
	"bytes"
	"fmt"
	"net"
	"sync"

	"github.com/emiago/sipgo/sip"
	"github.com/rs/zerolog/log"
)

type Connection interface {
	// WriteMsg marshals message and sends to socket
	WriteMsg(msg sip.Message) error
	// Reference of connection can be increased/decreased to prevent closing to earlyss
	Ref(i int)
	// Close decreases reference and if ref = 0 closes connection. Returns last ref. If 0 then it is closed
	TryClose() (int, error)

	Close() error
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

type conn struct {
	net.Conn

	transport string

	mu       sync.RWMutex
	refcount int
}

func (c *conn) Ref(i int) {
	// Not used so far
	c.mu.Lock()
	c.refcount += i
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().
		Str("transport", c.transport).
		Str("src", c.LocalAddr().String()).
		Str("dst", c.RemoteAddr().String()).
		Int("ref", ref).
		Msg("reference increment")
}

func (c *conn) Close() error {
	c.mu.Lock()
	c.refcount = 0
	c.mu.Unlock()
	log.Debug().
		Str("transport", c.transport).
		Str("src", c.LocalAddr().String()).
		Str("dst", c.RemoteAddr().String()).
		Int("ref", 0).
		Msg("doing hard close")
	return c.Conn.Close()
}

func (c *conn) TryClose() (int, error) {
	c.mu.Lock()
	c.refcount--
	ref := c.refcount
	c.mu.Unlock()
	log.Debug().
		Str("transport", c.transport).
		Str("src", c.LocalAddr().String()).
		Str("dst", c.RemoteAddr().String()).
		Int("ref", ref).
		Msg("TCP reference decrement")
	if ref > 0 {
		return ref, nil
	}

	if ref < 0 {
		log.Warn().
			Str("transport", c.transport).
			Str("src", c.LocalAddr().String()).
			Str("dst", c.RemoteAddr().String()).
			Int("ref", ref).
			Msg("ref went negative")
		return 0, nil
	}

	log.Debug().
		Str("transport", c.transport).
		Str("src", c.LocalAddr().String()).
		Str("dst", c.RemoteAddr().String()).
		Int("ref", ref).
		Msg("TCP closing")

	return ref, c.Conn.Close()
}

func (c *conn) String() string {
	return c.LocalAddr().Network() + ":" + c.LocalAddr().String()
}

func (c *conn) WriteMsg(msg sip.Message) error {
	return c.WriteMsgTo(msg, msg.Destination())
}

func (c *conn) WriteMsgTo(msg sip.Message, raddr string) error {
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
