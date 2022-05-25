package main

import (
	"sync"
)

type Registry struct {
	m map[string]string
	sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		m: make(map[string]string),
	}
}

func (r *Registry) Add(user, addr string) {
	r.Lock()
	r.m[user] = addr
	r.Unlock()
}

func (r *Registry) Get(user string) (addr string) {
	r.RLock()
	defer r.RUnlock()
	return r.m[user]
}
