package sip

import (
	"bytes"
	"errors"
	"net"
	"sync"

	"golang.org/x/sync/singleflight"
)

type Connection interface {
	// LocalAddr used for connection
	LocalAddr() net.Addr
	// WriteMsg marshals message and sends to socket
	WriteMsg(msg Message) error
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

type ConnectionPool struct {
	// TODO consider sync.Map way with atomic checks to reduce mutex contention
	sync.RWMutex
	m  map[string]Connection
	sf singleflight.Group
}

func NewConnectionPool() *ConnectionPool {
	p := &ConnectionPool{}
	p.init()
	return p
}

func (p *ConnectionPool) init() {
	p.m = make(map[string]Connection)
}

func (p *ConnectionPool) addSingleflight(raddr Addr, laddr Addr, reuse bool, do func() (Connection, error)) (Connection, error) {
	a := raddr.String()

	if laddr.Port > 0 || reuse {
		// TODO: remplement this here to avoid type conversion
		conn, err, shared := p.sf.Do(laddr.String()+raddr.String(), func() (any, error) {
			return do()
		})
		if err != nil {
			return nil, err
		}
		c := conn.(Connection)

		if shared {
			return c, nil
		}

		p.Lock()
		defer p.Unlock()

		p.m[a] = c
		p.m[c.LocalAddr().String()] = c
		return c, nil
	}

	// There is nothing here to block
	c, err := do()
	if err != nil {
		return nil, err
	}

	if c.Ref(0) < 1 {
		c.Ref(1) // Make 1 reference count by default
	}
	p.m[a] = c
	p.m[c.LocalAddr().String()] = c
	return c, nil
}

func (p *ConnectionPool) Add(a string, c Connection) {
	// TODO how about multi connection support for same remote address
	// We can then check ref count

	if c.Ref(0) < 1 {
		c.Ref(1) // Make 1 reference count by default
	}
	p.Lock()
	p.m[a] = c
	p.Unlock()
}

// Getting connection pool increases reference
// Make sure you TryClose after finish
func (p *ConnectionPool) Get(a string) (c Connection) {
	p.RLock()
	c, exists := p.m[a]
	p.RUnlock()
	if !exists {
		return nil
	}
	c.Ref(1)
	// TODO handling more references
	// if c.Ref(1) <= 1 {
	// 	return nil
	// }

	return c
}

// CloseAndDelete closes connection and deletes from pool
func (p *ConnectionPool) CloseAndDelete(c Connection, addr string) error {
	p.Lock()
	defer p.Unlock()
	delete(p.m, addr)
	ref, _ := c.TryClose() // Be nice. Saves from double closing
	if ref > 0 {
		return c.Close()
	}
	return nil
}

func (p *ConnectionPool) Delete(addr string) {
	p.Lock()
	defer p.Unlock()
	delete(p.m, addr)
}

func (p *ConnectionPool) DeleteMultiple(addrs []string) {
	p.Lock()
	defer p.Unlock()
	for _, a := range addrs {
		delete(p.m, a)
	}
}

// Clear will clear all connection from pool and close them
func (p *ConnectionPool) Clear() error {
	p.Lock()
	defer p.Unlock()

	defer func() {
		// Remove all
		p.m = make(map[string]Connection)
	}()

	var werr error
	for _, c := range p.m {
		if c.Ref(0) <= 0 {
			continue
		}
		werr = errors.Join(werr, c.Close())
	}
	return werr
}

func (p *ConnectionPool) Size() int {
	p.RLock()
	l := len(p.m)
	p.RUnlock()
	return l
}
