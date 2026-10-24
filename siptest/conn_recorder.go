package siptest

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
)

type connRecorder struct {
	mu   sync.Mutex
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
	c.mu.Lock()
	c.msgs = append(c.msgs, msg)
	c.mu.Unlock()
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
