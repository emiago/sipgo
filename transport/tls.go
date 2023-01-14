package transport

import (
	"crypto/tls"
	"fmt"
	"net"

	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog/log"
)

var ()

// TLS transport implementation
type TLSTransport struct {
	*TCPTransport

	// rootPool *x509.CertPool
	tlsConf *tls.Config
}

func NewTLSTransport(addr string, par parser.SIPParser, tlsConf *tls.Config) *TLSTransport {
	tcptrans := NewTCPTransport(addr, par)
	tcptrans.transport = TransportTLS //Override transport
	p := &TLSTransport{
		TCPTransport: tcptrans,
	}

	// p.rootPool = roots
	p.tlsConf = tlsConf
	p.log = log.Logger.With().Str("caller", "transport<TLS>").Logger()
	return p
}

func (t *TLSTransport) String() string {
	return "transport<TLS>"
}

// This is more generic way to provide listener and it is blocking
func (t *TLSTransport) ListenAndServe(handler sip.MessageHandler) error {
	addr := t.addr
	laddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return fmt.Errorf("fail to resolve address. err=%w", err)
	}

	listener, err := tls.Listen("tcp", laddr.String(), t.tlsConf.Clone())
	if err != nil {
		return fmt.Errorf("listen tls error. err=%w", err)
	}

	return t.Serve(listener, handler)
}

// CreateConnection creates TLS connection for TCP transport
func (t *TLSTransport) CreateConnection(addr string) (Connection, error) {
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	return t.createConnection(raddr)
}

func (t *TLSTransport) createConnection(raddr *net.TCPAddr) (Connection, error) {
	addr := raddr.String()
	t.log.Debug().Str("raddr", addr).Msg("Dialing new connection")

	//TODO does this need to be each config
	// SHould we make copy of rootPool?
	// There is Clone of config

	conn, err := tls.Dial("tcp", raddr.String(), t.tlsConf)
	if err != nil {
		return nil, fmt.Errorf("%s dial err=%w", t, err)
	}

	c := t.initConnection(conn, addr)
	return c, nil
}
