package transport

import (
	"log/slog"
	"sync"
)

// TODO Connection pool with keeping active connections longer

type ConnectionPool struct {
	// TODO consider sync.Map way with atomic checks to reduce mutex contention
	sync.RWMutex
	m map[string]Connection
}

func NewConnectionPool() *ConnectionPool {
	return &ConnectionPool{
		m: make(map[string]Connection),
	}
}

// Add registers a connection for an address. One connection per address: the
// last one added wins, so a duplicate silently orphans the previous one.
// Layer.createConnection keeps concurrent requests from dialing duplicates.
// The pool owns one reference to c, released by CloseAndDelete.
func (p *ConnectionPool) Add(a string, c Connection) {
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
func (p *ConnectionPool) CloseAndDelete(log *slog.Logger, c Connection, addr string) {
	p.Lock()
	defer p.Unlock()
	ref, _ := c.TryClose() // Release the pool's reference.
	if ref > 0 {
		if err := c.Close(); err != nil {
			log.Info("Closing connection returned error", "err", err)
		}
	}
	// A newer connection may have replaced c under addr by now.
	if p.m[addr] == c {
		delete(p.m, addr)
	}
}

// Clear will clear all connection from pool and close them
func (p *ConnectionPool) Clear(log *slog.Logger) {
	p.Lock()
	defer p.Unlock()
	for _, c := range p.m {
		if c.Ref(0) <= 0 {
			continue
		}
		if err := c.Close(); err != nil {
			log.Info("Closing connection returned error", "err", err)
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
