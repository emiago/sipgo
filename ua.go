package sipgo

import (
	"context"
	"net"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transaction"
	"github.com/emiago/sipgo/transport"
)

type UserAgent struct {
	name string
	ip   net.IP
	host string
	port int

	dnsResolver *net.Resolver
	tp          *transport.Layer
	tx          *transaction.Layer
}

type UserAgentOption func(s *UserAgent) error

func WithUserAgent(ua string) UserAgentOption {
	return func(s *UserAgent) error {
		s.name = ua
		return nil
	}
}

func WithIP(ip string) UserAgentOption {
	return func(s *UserAgent) error {
		host, _, err := net.SplitHostPort(ip)
		if err != nil {
			return err
		}
		addr, err := net.ResolveIPAddr("ip", host)
		if err != nil {
			return err
		}
		return s.setIP(addr.IP)
	}
}

func WithDNSResolver(r *net.Resolver) UserAgentOption {
	return func(s *UserAgent) error {
		s.dnsResolver = r
		return nil
	}
}

func WithUDPDNSResolver(dns string) ServerOption {
	return func(s *Server) error {
		s.dnsResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, "udp", dns)
			},
		}
		return nil
	}
}

func NewUA(options ...UserAgentOption) (*UserAgent, error) {
	s := &UserAgent{}

	for _, o := range options {
		if err := o(s); err != nil {
			return nil, err
		}
	}

	if s.ip == nil {
		v, err := sip.ResolveSelfIP()
		if err != nil {
			return nil, err
		}
		if err := s.setIP(v); err != nil {
			return nil, err
		}
	}

	s.tp = transport.NewLayer(s.dnsResolver)
	s.tx = transaction.NewLayer(s.tp)
	return s, nil
}

// Listen adds listener for serve
func (ua *UserAgent) setIP(ip net.IP) (err error) {
	ua.ip = ip
	ua.host = strings.Split(ip.String(), ":")[0]
	return err
}
