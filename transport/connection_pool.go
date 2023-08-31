package transport

import (
	"sync"

	"github.com/rs/zerolog/log"
)

// TODO Connection pool with keeping active connections longer

type ConnectionPool struct {
	// TODO consider sync.Map way with atomic checks to reduce mutex contention
	sync.RWMutex
	m map[string]Connection
}

func NewConnectionPool() ConnectionPool {
	return ConnectionPool{
		m: make(map[string]Connection),
	}
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
func (p *ConnectionPool) CloseAndDelete(c Connection, addr string) {
	p.Lock()
	defer p.Unlock()
	ref, _ := c.TryClose() // Be nice. Saves from double closing
	if ref > 0 {
		if err := c.Close(); err != nil {
			log.Warn().Err(err).Msg("Closing conection return error")
		}
	}
	delete(p.m, addr)
}

// Clear will clear all connection from pool and close them
func (p *ConnectionPool) Clear() {
	p.Lock()
	defer p.Unlock()
	for _, c := range p.m {
		if c.Ref(0) <= 0 {
			continue
		}
		if err := c.Close(); err != nil {
			log.Warn().Err(err).Msg("Closing conection return error")
		}
	}
	// Remove all
	p.m = make(map[string]Connection)
}

func (p *ConnectionPool) Size() int {
	p.RLock()
	l := len(p.m)
	p.RUnlock()
	return l
}
