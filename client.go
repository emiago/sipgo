package sipgo

import (
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

	return c, nil
}

// TransactionRequest uses transaction layer to send request
// Customizing req can be done via options. NOTE: this overrides default header construction
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
// Customizing req can be done via options same like TransactionRequest
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
	ClientRequestAddVia(c, req)

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
		}
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

	// TODO: Add MaxForwads shortcut
	if h := req.GetHeader("Max-Forwards"); h == nil {
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
		Next:            nil,
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

// TODO
// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261#section-16
func ClientRequestDecreaseMaxForward(c *Client, r *sip.Request) error {
	// TODO
	return nil
}

// ClientResponseRemoveVia is needed when handling client transaction response, where previously used in
// TransactionRequest with ClientRequestAddVia
func ClientResponseRemoveVia(c *Client, r *sip.Response) {
	// var removedFromMulti bool
	// Faster access TODO
	// if via, exists := r.Via(); exists {
	// 	prevvia := via
	// 	for via != nil {
	// 		if via.Host == c.host {

	// 			removedFromMulti = true
	// 			break
	// 		}
	// 		via = via.Next
	// 		prevvia = via
	// 	}
	// }
	// if !removedFromMulti {
	// 	r.RemoveHeaderOn("Via", c.removeViaCallback)
	// }

	r.RemoveHeaderOn("Via", c.removeViaCallback)
}

func (c *Client) removeViaCallback(h sip.Header) bool {
	via := h.(*sip.ViaHeader)

	// Check is this multivalue
	// If yes then just remove that value
	// TODO can this be done better
	if via.Next != nil {
		prevvia := via
		for via != nil {
			if via.Host == c.host {
				prevvia.Next = via.Next
				via.Next = nil
				return false
			}
			prevvia = via
			via = via.Next
		}
	}

	return via.Host == c.host
}
