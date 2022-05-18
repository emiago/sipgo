package sip

import (
	"net"
	"strconv"
)

type Transport interface {
	WriteMsg(msg Message) error
}

func ParseAddr(addr string) (host string, port int, err error) {
	host, pstr, err := net.SplitHostPort(addr)
	if err != nil {
		return host, port, err
	}

	port, err = strconv.Atoi(pstr)
	return host, port, err
}
