package transport

import (
	"bytes"
	"fmt"
	"net"

	"github.com/emiraganov/sipgo/sip"
)

type Connection interface {
	WriteMsg(msg sip.Message) error
	Close() error
}

type Conn struct {
	net.Conn
}

func (c *Conn) String() string {
	return c.LocalAddr().Network() + ":" + c.LocalAddr().String()
}

func (c *Conn) WriteMsg(msg sip.Message) error {
	return c.WriteMsgTo(msg, msg.Destination())
}

func (c *Conn) WriteMsgTo(msg sip.Message, raddr string) error {
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
