package sipgo

import (
	"fmt"
	"net"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init() {
	uuid.EnableRandPool()
}

type Client struct {
	*UserAgent
	host string
	port int
	log  zerolog.Logger
}

type ClientOption func(c *Client) error

// WithClientLogger allows customizing client logger
func WithClientLogger(logger zerolog.Logger) ClientOption {
	return func(s *Client) error {
		s.log = logger
		return nil
	}
}

// WithClientHost allows setting default route host
// default it will be used user agent IP
func WithClientHost(host string) ClientOption {
	return func(s *Client) error {
		s.host = host
		return nil
	}
}

// WithClientPort allows setting default route port
func WithClientPort(port int) ClientOption {
	return func(s *Client) error {
		s.port = port
		return nil
	}
}

// NewClient creates client handle for user agent
func NewClient(ua *UserAgent, options ...ClientOption) (*Client, error) {
	c := &Client{
		UserAgent: ua,
		host:      ua.GetIP().String(),
		log:       log.Logger.With().Str("caller", "Client").Logger(),
	}

	for _, o := range options {
		if err := o(c); err != nil {
			return nil, err
		}
	}

	return c, nil
}

// TransactionRequest uses transaction layer to send request and returns transaction
// By default the following header fields will be added if not exist:
// To, From, CSeq, Call-ID, Max-Forwards, Via
// Passing custom options will override this behavior.
// This is useful when using client handle in proxy building
//
// NOTE: request will not be cloned and header adding will be present after this action
func (c *Client) TransactionRequest(req *sip.Request, options ...ClientRequestOption) (sip.ClientTransaction, error) {
	if len(options) == 0 {
		clientRequestBuildReq(c, req)
		return c.tx.Request(req)
	}

	for _, o := range options {
		if err := o(c, req); err != nil {
			return nil, err
		}
	}
	return c.tx.Request(req)
}

// WriteRequest sends request directly to transport layer
// Behavior is same as TransactionRequest
// Non-transaction ACK request should be passed like this
func (c *Client) WriteRequest(req *sip.Request, options ...ClientRequestOption) error {
	if len(options) == 0 {
		clientRequestBuildReq(c, req)
		return c.tp.WriteMsg(req)
	}

	for _, o := range options {
		if err := o(c, req); err != nil {
			return err
		}
	}
	return c.tp.WriteMsg(req)
}

type ClientRequestOption func(c *Client, req *sip.Request) error

func clientRequestBuildReq(c *Client, req *sip.Request) error {
	// https://www.rfc-editor.org/rfc/rfc3261#section-8.1.1
	// A valid SIP request formulated by a UAC MUST, at a minimum, contain
	// the following header fields: To, From, CSeq, Call-ID, Max-Forwards,
	// and Via;
	if _, exists := req.Via(); !exists {
		// Multi VIA value must be manually added
		ClientRequestAddVia(c, req)
	}

	if _, exists := req.From(); !exists {
		from := sip.FromHeader{
			DisplayName: c.name,
			Address: sip.Uri{
				User:      c.name,
				Host:      c.host,
				Port:      c.port,
				UriParams: sip.NewParams(),
				Headers:   sip.NewParams(),
			},
			Params: sip.NewParams(),
		}
		from.Params.Add("tag", sip.GenerateTagN(16))
		req.AppendHeader(&from)
	}

	if _, exists := req.To(); !exists {
		to := sip.ToHeader{
			Address: sip.Uri{
				Encrypted: req.Recipient.Encrypted,
				User:      req.Recipient.User,
				Host:      req.Recipient.Host,
				Port:      req.Recipient.Port,
				UriParams: req.Recipient.UriParams,
				Headers:   req.Recipient.Headers,
			},
			Params: sip.NewParams(),
		}
		req.AppendHeader(&to)
	}

	if _, exists := req.CallID(); !exists {
		uuid, err := uuid.NewRandom()
		if err != nil {
			return err
		}

		callid := sip.CallIDHeader(uuid.String())
		req.AppendHeader(&callid)

	}

	if _, exists := req.CSeq(); !exists {
		// TODO consider atomic increase cseq within Dialog
		cseq := sip.CSeqHeader{
			SeqNo:      1,
			MethodName: req.Method,
		}
		req.AppendHeader(&cseq)
	}

	if _, exists := req.MaxForwards(); !exists {
		maxfwd := sip.MaxForwardsHeader(70)
		req.AppendHeader(&maxfwd)
	}

	if req.Body() == nil {
		req.SetBody(nil)
	}

	return nil
}

// ClientRequestAddVia is option for adding via header
// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261.html#section-16.6
func ClientRequestAddVia(c *Client, r *sip.Request) error {
	newvia := &sip.ViaHeader{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       r.Transport(),
		Host:            c.host,
		Port:            c.port,
		Params:          sip.NewParams(),
	}
	// NOTE: Consider lenght of branch configurable
	newvia.Params.Add("branch", sip.GenerateBranchN(16))

	if via, exists := r.Via(); exists {
		// newvia.Params.Add("branch", via.Params["branch"])
		if via.Params.Has("rport") {
			h, p, _ := net.SplitHostPort(r.Source())
			via.Params.Add("rport", p)
			via.Params.Add("received", h)
		}
	}
	r.PrependHeader(newvia)
	return nil
}

// ClientRequestAddRecordRoute is option for adding record route header
// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261#section-16
func ClientRequestAddRecordRoute(c *Client, r *sip.Request) error {
	rr := &sip.RecordRouteHeader{
		Address: sip.Uri{
			Host: c.host,
			Port: c.port,
			UriParams: sip.HeaderParams{
				// Transport must be provided as wesll
				// https://datatracker.ietf.org/doc/html/rfc5658
				"transport": transport.NetworkToLower(r.Transport()),
				"lr":        "",
			},
		},
	}

	r.PrependHeader(rr)
	return nil
}

// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261#section-16
// ClientRequestDecreaseMaxForward should be used when forwarding request. It decreases max forward
// in case of 0 it returnes error
func ClientRequestDecreaseMaxForward(c *Client, r *sip.Request) error {
	maxfwd, exists := r.MaxForwards()
	if !exists {
		// TODO, should we return error here
		return nil
	}

	maxfwd.Dec()

	if maxfwd.Val() <= 0 {
		return fmt.Errorf("Max forwards reached")
	}
	return nil
}

// ClientResponseRemoveVia is needed when handling client transaction response, where previously used in
// TransactionRequest with ClientRequestAddVia
func ClientResponseRemoveVia(c *Client, r *sip.Response) {
	via, exists := r.Via()
	if !exists {
		return
	}
	if via.Host == c.host {
		r.RemoveHeader("Via")
	}
}
