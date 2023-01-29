package sipgo

import (
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

func WithUserAgentIP(ip string) UserAgentOption {
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

func WithUserAgentDNSResolver(r *net.Resolver) UserAgentOption {
	return func(s *UserAgent) error {
		s.dnsResolver = r
		return nil
	}
}

func NewUA(options ...UserAgentOption) (*UserAgent, error) {
	ua := &UserAgent{}

	for _, o := range options {
		if err := o(ua); err != nil {
			return nil, err
		}
	}

	if ua.ip == nil {
		v, err := sip.ResolveSelfIP()
		if err != nil {
			return nil, err
		}
		if err := ua.setIP(v); err != nil {
			return nil, err
		}
	}

	ua.tp = transport.NewLayer(ua.dnsResolver)
	ua.tx = transaction.NewLayer(ua.tp)
	return ua, nil
}

// Listen adds listener for serve
func (ua *UserAgent) setIP(ip net.IP) (err error) {
	ua.ip = ip
	ua.host = strings.Split(ip.String(), ":")[0]
	return err
}