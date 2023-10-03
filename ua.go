package sipgo

import (
	"crypto/tls"
	"net"

	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transaction"
	"github.com/emiago/sipgo/transport"
)

type UserAgent struct {
	name        string
	ip          net.IP
	dnsResolver *net.Resolver
	tlsConfig   *tls.Config
	tp          *transport.Layer
	tx          *transaction.Layer
}

type UserAgentOption func(s *UserAgent) error

// WithUserAgent changes user agent name
// Default: sipgo
func WithUserAgent(ua string) UserAgentOption {
	return func(s *UserAgent) error {
		s.name = ua
		return nil
	}
}

// WithUserAgentIP sets local IP that will be used in building request
// If not used IP will be resolved
// Deprecated: Use on client WithClientHostname WithClientPort
func WithUserAgentIP(ip net.IP) UserAgentOption {
	return func(s *UserAgent) error {
		return s.setIP(ip)
	}
}

// WithUserAgentDNSResolver allows customizing default DNS resolver for transport layer
func WithUserAgentDNSResolver(r *net.Resolver) UserAgentOption {
	return func(s *UserAgent) error {
		s.dnsResolver = r
		return nil
	}
}

// WithUserAgenTLSConfig allows customizing default tls config.
func WithUserAgenTLSConfig(c *tls.Config) UserAgentOption {
	return func(s *UserAgent) error {
		s.tlsConfig = c
		return nil
	}
}

// NewUA creates User Agent
// User Agent will create transport and transaction layer
// Check options for customizing user agent
func NewUA(options ...UserAgentOption) (*UserAgent, error) {
	ua := &UserAgent{
		name:        "sipgo",
		dnsResolver: net.DefaultResolver,
	}

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

	// TODO export parser to be configurable
	ua.tp = transport.NewLayer(ua.dnsResolver, parser.NewParser(), ua.tlsConfig)
	ua.tx = transaction.NewLayer(ua.tp)
	return ua, nil
}

func (ua *UserAgent) Close() error {
	// stop transaction layer
	ua.tx.Close()

	// stop transport layer
	return ua.tp.Close()
}

// Listen adds listener for serve
func (ua *UserAgent) setIP(ip net.IP) (err error) {
	ua.ip = ip
	return err
}

func (ua *UserAgent) GetIP() net.IP {
	return ua.ip
}

func (ua *UserAgent) TransportLayer() *transport.Layer {
	return ua.tp
}
