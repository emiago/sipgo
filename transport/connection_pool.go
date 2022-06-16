package transport

import (
	"net"
	"sync"
)

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
	p.Lock()
	p.m[a] = c
	p.Unlock()
}

func (p *ConnectionPool) Get(a string) (c Connection) {
	p.RLock()
	c = p.m[a]
	p.RUnlock()
	return c
}

func (p *ConnectionPool) Del(a string) {
	p.Lock()
	delete(p.m, a)
	p.Unlock()
}

type TCPPool struct {
	sync.RWMutex
	m map[string]*net.TCPConn
}

func NewTCPPool() TCPPool {
	return TCPPool{
		m: make(map[string]*net.TCPConn),
	}
}

func (p *TCPPool) Add(a string, c *net.TCPConn) {
	p.Lock()
	p.m[a] = c
	p.Unlock()
}

func (p *TCPPool) Get(a string) (c *net.TCPConn) {
	p.RLock()
	c = p.m[a]
	p.RUnlock()
	return c
}

func (p *TCPPool) Del(a string) {
	p.Lock()
	delete(p.m, a)
	p.Unlock()
}
