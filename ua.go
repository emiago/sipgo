package sipgo

import (
	"crypto/tls"
	"net"

	"github.com/emiago/sipgo/sip"
)

type UserAgent struct {
	name        string
	hostname    string
	ip          net.IP
	dnsResolver *net.Resolver
	tlsConfig   *tls.Config
	parser      *sip.Parser
	tp          *sip.TransportLayer
	tx          *sip.TransactionLayer
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

// WithUserAgentHostname represents FQDN of user that can be presented in From header
func WithUserAgentHostname(hostname string) UserAgentOption {
	return func(s *UserAgent) error {
		s.hostname = hostname
		return nil
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

func WithUserAgentParser(p *sip.Parser) UserAgentOption {
	return func(s *UserAgent) error {
		s.parser = p
		return nil
	}
}

// NewUA creates User Agent
// User Agent will create transport and transaction layer
// Check options for customizing user agent
func NewUA(options ...UserAgentOption) (*UserAgent, error) {
	ua := &UserAgent{
		name: "sipgo",
		// hostname:    "localhost",
		dnsResolver: net.DefaultResolver,
		parser:      sip.NewParser(),
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

	ua.tp = sip.NewTransportLayer(ua.dnsResolver, ua.parser, ua.tlsConfig)
	ua.tx = sip.NewTransactionLayer(ua.tp)
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

func (ua *UserAgent) Name() string {
	return ua.name
}

func (ua *UserAgent) TransportLayer() *sip.TransportLayer {
	return ua.tp
}

func (ua *UserAgent) TransactionLayer() *sip.TransactionLayer {
	return ua.tx
}
