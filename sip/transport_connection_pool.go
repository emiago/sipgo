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

// ConnectionPool stores connections by address. A connection may have multiple
// address entries. Obtain a transport's live pool through TransportLayer.Pool.
type ConnectionPool struct {
	// TODO consider sync.Map way with atomic checks to reduce mutex contention
	mu sync.RWMutex
	m  map[string]Connection
	sf singleflight.Group
}

func newConnectionPool() *ConnectionPool {
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

			p.mu.Lock()
			defer p.mu.Unlock()

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
	p.mu.Lock()
	p.m[a] = c
	p.m[c.LocalAddr().String()] = c
	p.mu.Unlock()
	return c, nil
}

func (p *ConnectionPool) Add(a string, c Connection) {
	// TODO how about multi connection support for same remote address
	// We can then check ref count

	if c.Ref(0) < 1 {
		c.Ref(1) // Make 1 reference count by default
	}
	p.mu.Lock()
	p.m[a] = c
	p.mu.Unlock()
}

// Getting connection pool increases reference
// Make sure you TryClose after finish
func (p *ConnectionPool) Get(a string) (c Connection) {
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

func (p *ConnectionPool) getUnref(a string) (c Connection) {
	p.mu.RLock()
	c, exists := p.m[a]
	p.mu.RUnlock()
	if !exists {
		return nil
	}
	return c
}

// CloseAndDelete closes c and removes addr only if it still refers to c.
// A replacement connection registered at addr is left in the pool.
func (p *ConnectionPool) CloseAndDelete(c Connection, addr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleteIfCurrentLocked(addr, c)
	ref, _ := c.TryClose() // Be nice. Saves from double closing
	if ref > 0 {
		return c.Close()
	}
	return nil
}

// Delete removes addr regardless of which connection it refers to.
func (p *ConnectionPool) Delete(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, addr)
}

// DeleteMultiple removes the addresses regardless of which connections they refer to.
func (p *ConnectionPool) DeleteMultiple(addrs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range addrs {
		delete(p.m, a)
	}
}

func (p *ConnectionPool) deleteExact(addr string, c Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleteIfCurrentLocked(addr, c)
}

func (p *ConnectionPool) deleteExactN(addrs []string, c Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, addr := range addrs {
		p.deleteIfCurrentLocked(addr, c)
	}
}

// deleteIfCurrentLocked requires p.mu to be held for writing.
func (p *ConnectionPool) deleteIfCurrentLocked(addr string, c Connection) {
	if p.m[addr] == c {
		delete(p.m, addr)
	}
}

// Clear will clear all connection from pool and close them
func (p *ConnectionPool) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()

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

// Size returns the number of address entries in the pool. A connection may
// occupy more than one entry.
func (p *ConnectionPool) Size() int {
	p.mu.RLock()
	l := len(p.m)
	p.mu.RUnlock()
	return l
}
