package siptest

import (
	"net"
	"sync/atomic"

	"github.com/shpendbeqiraj2/sipgo/sip"
)

type connRecorder struct {
	msgs []sip.Message

	ref atomic.Int32
}

func newConnRecorder() *connRecorder {
	return &connRecorder{}
}

func (c *connRecorder) LocalAddr() net.Addr {
	return nil
}

func (c *connRecorder) WriteMsg(msg sip.Message) error {
	c.msgs = append(c.msgs, msg)
	return nil
}
func (c *connRecorder) Ref(i int) int {
	return int(c.ref.Add(int32(i)))
}
func (c *connRecorder) TryClose() (int, error) {
	new := c.ref.Add(int32(-1))
	return int(new), nil
}
func (c *connRecorder) Close() error { return nil }
