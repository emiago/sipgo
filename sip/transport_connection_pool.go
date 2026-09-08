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

type connectionPool struct {
	// TODO consider sync.Map way with atomic checks to reduce mutex contention
	sync.RWMutex
	m  map[string]Connection
	sf singleflight.Group
}

func newConnectionPool() *connectionPool {
	p := &connectionPool{}
	p.init()
	return p
}

func (p *connectionPool) init() {
	p.m = make(map[string]Connection)
}

func (p *connectionPool) addSingleflight(raddr Addr, laddr Addr, reuse bool, do func() (Connection, error)) (Connection, error) {
	a := raddr.String()

	if laddr.Port > 0 || reuse {
		// TODO: implement singleflight without  type conversion
		laddrStr := laddr.String()
		// We create or return existing
		conn, err, _ := p.sf.Do(laddrStr+a, func() (any, error) {
			if laddr.Port > 0 {
				if c := p.getUnref(laddrStr); c != nil {
					return c, nil
				}
			} else {
				if c := p.getUnref(a); c != nil {
					return c, nil
				}
			}

			c, err := do()
			if err != nil {
				return nil, err
			}
			// Decrease reference as it will be increased after
			// Singleflight will return cached so we need todo this
			c.Ref(-1)

			p.Lock()
			defer p.Unlock()

			p.m[a] = c
			p.m[c.LocalAddr().String()] = c
			return c, nil
		})
		if err != nil {
			return nil, err
		}
		c := conn.(Connection)
		c.Ref(1)
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

func (p *connectionPool) Add(a string, c Connection) {
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
func (p *connectionPool) Get(a string) (c Connection) {
	// p.RLock()
	// c, exists := p.m[a]
	// p.RUnlock()
	// if !exists {
	// 	return nil
	// }
	c = p.getUnref(a)
	if c == nil {
		return nil
	}
	c.Ref(1)
	return c
}

func (p *connectionPool) getUnref(a string) (c Connection) {
	p.RLock()
	c, exists := p.m[a]
	p.RUnlock()
	if !exists {
		return nil
	}
	// A closed connection can still sit in the pool: one connection is stored
	// under several keys (remote and local address, and for the UDP listener
	// every peer it accepted), while closing removes them one at a time.
	// Handing such a connection out sends the caller to a socket that is
	// already gone instead of letting it dial a new one, and Ref pulls the
	// count of a closed connection back above zero, so the next TryClose
	// closes it a second time. Asked through an anonymous interface to keep
	// the exported Connection interface unchanged.
	if closer, ok := c.(interface{ Closed() bool }); ok && closer.Closed() {
		return nil
	}
	return c
}

// CloseAndDelete deletes connection from pool and releases the reference held
// by its reader.
//
// It used to force a hard Close whenever references remained, which defeats
// the reference counting the pool is built on: the socket is taken away from
// the owners still holding it, and Close resets refcount to 0, so their later
// TryClose runs on a connection that is already gone. TryClose closes as soon
// as the last reference is released, which is the only point where closing is
// safe. Readers give up the idle reference (TransportIdleConnection) before
// calling this, so a connection leaving the pool still reaches zero.
func (p *connectionPool) CloseAndDelete(c Connection, addr string) error {
	p.Lock()
	defer p.Unlock()
	delete(p.m, addr)
	_, err := c.TryClose()
	return err
}

func (p *connectionPool) Delete(addr string) {
	p.Lock()
	defer p.Unlock()
	delete(p.m, addr)
}

func (p *connectionPool) DeleteMultiple(addrs []string) {
	p.Lock()
	defer p.Unlock()
	for _, a := range addrs {
		delete(p.m, a)
	}
}

// Clear will clear all connection from pool and close them
func (p *connectionPool) Clear() error {
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

func (p *connectionPool) Size() int {
	p.RLock()
	l := len(p.m)
	p.RUnlock()
	return l
}
