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
func (c *Client) TransactionRequest(req *sip.Request, options ...ClientRequestOption) (sip.ClientTransaction, error) {
	for _, o := range options {
		if err := o(c, req); err != nil {
			return nil, err
		}
	}
	return c.tx.Request(req)
}

// WriteRequest sends request directly to transport layer
func (c *Client) WriteRequest(req *sip.Request) error {
	return c.tp.WriteMsg(req)
}

type ClientRequestOption func(c *Client, req *sip.Request) error

// ClientRequestAddVia is option for adding via header
// https://www.rfc-editor.org/rfc/rfc3261.html#section-16.6
func ClientRequestAddVia(c *Client, r *sip.Request) error {
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
	return nil
}

// ClientRequestAddRecordRoute is option for adding record route header
func ClientRequestAddRecordRoute(c *Client, r *sip.Request) error {
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
	return nil
}

// ClientResponseRemoveVia is needed when handling client transaction response, where previously used in
// TransactionRequest with ClientRequestAddVia
func ClientResponseRemoveVia(c *Client, r *sip.Response) {
	if via, exists := r.Via(); exists {
		if via.Host == c.host {
			// In case it is multipart Via remove only one
			if via.Next != nil {
				via.Remove()
			} else {
				r.RemoveHeader("Via")
			}
		}
	}
}
