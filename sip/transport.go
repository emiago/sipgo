package sip

import (
	"net"
	"strconv"
)

type IPAddr struct {
	IP   net.IP
	Port int
}

type Transport interface {
	WriteMsg(msg Message) error
}

func ParseAddr(addr string) (host string, port int, err error) {
	host, pstr, err := net.SplitHostPort(addr)
	if err != nil {
		return host, port, err
	}

	// In case we are dealing with some named ports this should be called
	// net.LookupPort(network)

	port, err = strconv.Atoi(pstr)
	return host, port, err
}
