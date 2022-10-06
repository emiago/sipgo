package sipgo

import (
	"net"

	"github.com/emiraganov/sipgo/sip"
	"github.com/emiraganov/sipgo/transport"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Client struct {
	*UserAgent
	log zerolog.Logger

	AddViaHeader   bool
	AddRecordRoute bool
}

type ClientOption func(c *Client) error

func NewClient(ua *UserAgent, options ...ClientOption) (*Client, error) {
	c := &Client{
		UserAgent: ua,
		log:       log.Logger.With().Str("caller", "Client").Logger(),
	}

	for _, o := range options {
		if err := o(c); err != nil {
			return nil, err
		}
	}

	// c.tx = transaction.NewLayer(c.tp)
	return c, nil
}

// TransactionRequest uses transaction layer to send request
func (c *Client) TransactionRequest(req *sip.Request) (sip.ClientTransaction, error) {
	c.updateRequest(req)
	return c.tx.Request(req)
}

func (c *Client) WriteRequest(req *sip.Request) error {
	return c.tp.WriteMsg(req)
}

func (c *Client) updateRequest(r *sip.Request) {
	// We handle here only INVITE and BYE
	// https://www.rfc-editor.org/rfc/rfc3261.html#section-16.6
	if c.AddViaHeader {
		if via, exists := r.Via(); exists {
			newvia := via.Clone()
			newvia.Host = c.host
			newvia.Port = c.port
			r.PrependHeader(newvia)

			if via.Params.Has("rport") {
				h, p, _ := net.SplitHostPort(r.Source())
				via.Params.Add("rport", p)
				via.Params.Add("received", h)
			}
		}
	}

	if c.AddRecordRoute {
		rr := &sip.RecordRouteHeader{
			Address: sip.Uri{
				Host: c.host,
				Port: c.port,
				UriParams: sip.HeaderParams{
					// Transport must be provided as well
					// https://datatracker.ietf.org/doc/html/rfc5658
					"transport": transport.NetworkToLower(r.Transport()),
					"lr":        "",
				},
			},
		}

		r.PrependHeader(rr)
	}
}
