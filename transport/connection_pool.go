package transport

import (
	"sync"
)

// TODO Connection pool with keeping active connections longer
type ConnectionPool struct {
	sync.RWMutex
	m map[string]Connection
}

func NewConnectionPool() ConnectionPool {
	return ConnectionPool{
		m: make(map[string]Connection),
	}
}

func (p *ConnectionPool) Add(a string, c Connection) {
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
	// Reference could drop with TryClose before it get deleted
	// NOTE: DEADLOCK! if used inside pool lock
	if c.Ref(1) <= 1 {
		return nil
	}

	return c
}

// CloseAndDelete closes connection and deletes from pool
func (p *ConnectionPool) CloseAndDelete(c Connection, addr string) {
	ref, _ := c.TryClose() // Be nice. Saves from double closing
	if ref > 0 {
		c.Close()
		return
	}
	p.Lock()
	delete(p.m, addr)
	p.Unlock()
}

func (p *ConnectionPool) Size() int {
	p.RLock()
	l := len(p.m)
	p.RUnlock()
	return l
}
