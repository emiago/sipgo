package transport

import (
	"bytes"
	"net"
	"sync"

	"github.com/emiago/sipgo/sip"
)

type Connection interface {
	// LocalAddr used for connection
	LocalAddr() net.Addr
	// WriteMsg marshals message and sends to socket
	WriteMsg(msg sip.Message) error
	// Reference of connection can be increased/decreased to prevent closing to earlyss
	Ref(i int) int
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
