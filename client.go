package sipgo

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"github.com/icholy/digest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init() {
	uuid.EnableRandPool()
}

type Client struct {
	*UserAgent
	host  string
	port  int
	rport bool
	log   zerolog.Logger
}

type ClientOption func(c *Client) error

// WithClientLogger allows customizing client logger
func WithClientLogger(logger zerolog.Logger) ClientOption {
	return func(s *Client) error {
		s.log = logger
		return nil
	}
}

// WithClientHost allows setting default route host or IP on Via
// in case of IP it will enforce transport layer to create/reuse connection with this IP
// default: user agent IP
// This is useful when you need to act as client first and avoid creating server handle listeners.
// NOTE: From header hostname is WithUserAgentHostname option on UA or modify request manually
func WithClientHostname(hostname string) ClientOption {
	return func(s *Client) error {
		s.host = hostname
		return nil
	}
}

// WithClientPort allows setting default route Via port
// it will enforce transport layer to create connection with this port if does NOT exist
// transport layer will choose existing connection by default unless
// TransportLayer.ConnectionReuse is set to false
// default: ephemeral port
func WithClientPort(port int) ClientOption {
	return func(s *Client) error {
		s.port = port
		return nil
	}
}

// WithClientNAT makes client aware that is behind NAT.
func WithClientNAT() ClientOption {
	return func(s *Client) error {
		s.rport = true
		return nil
	}
}

// WithClientAddr is merge of WithClientHostname and WithClientPort
// addr is format <host>:<port>
func WithClientAddr(addr string) ClientOption {
	return func(s *Client) error {
		host, port, err := sip.ParseAddr(addr)
		if err != nil {
			return err
		}

		WithClientHostname(host)(s)
		WithClientPort(port)(s)
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

// Close client handle. UserAgent must be closed for full transaction and transport layer closing.
func (c *Client) Close() error {
	return nil
}

func (c *Client) GetHostname() string {
	return c.host
}

// TransactionRequest uses transaction layer to send request and returns transaction
//
// NOTE: By default request will not be cloned and it will populate request with missing headers unless options are used
// In most cases you want this as you will retry with additional headers
//
// Following header fields will be added if not exist to have correct SIP request:
// To, From, CSeq, Call-ID, Max-Forwards, Via
//
// Passing options will override this behavior, that is it is expected
// that you have request fully built
// This is useful when using client handle in proxy building as request are already parsed
func (c *Client) TransactionRequest(ctx context.Context, req *sip.Request, options ...ClientRequestOption) (sip.ClientTransaction, error) {
	if req.IsAck() {
		return nil, fmt.Errorf("ACK request must be sent directly through transport. Use WriteRequest")
	}

	if len(options) == 0 {
		if cseq := req.CSeq(); cseq != nil {
			// Increase cseq if this is existing transaction
			// WriteRequest for ex ACK will not increase and this is wanted behavior
			// This will be a problem if we allow ACK to be passed as transaction request
			cseq.SeqNo++
			cseq.MethodName = req.Method
		}

		clientRequestBuildReq(c, req)
		return c.tx.Request(ctx, req)
	}

	for _, o := range options {
		if err := o(c, req); err != nil {
			return nil, err
		}
	}
	return c.tx.Request(ctx, req)
}

// Experimental
//
// Do request is HTTP client like Do request/response
// It returns on final response.
// Canceling ctx sends Cancel Request but it still returns ctx error
// For more control use TransactionRequest
func (c *Client) Do(ctx context.Context, req *sip.Request, options ...ClientRequestOption) (*sip.Response, error) {
	tx, err := c.TransactionRequest(ctx, req, options...)
	if err != nil {
		return nil, err
	}
	defer tx.Terminate()

	for {
		select {
		case res := <-tx.Responses():
			if res.IsProvisional() {
				continue
			}
			return res, nil

		case <-tx.Done():
			return nil, tx.Err()

		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), tx.Cancel())
		}
	}
}

type DigestAuth struct {
	Username string
	Password string
}

// DoDigestAuth will apply digest authentication if initial request is chalenged by 401 or 407.
// It returns new transaction that is created for this request
func (c *Client) DoDigestAuth(ctx context.Context, req *sip.Request, res *sip.Response, auth DigestAuth) (sip.ClientTransaction, error) {
	if res.StatusCode == sip.StatusProxyAuthRequired {
		return digestProxyAuthRequest(ctx, c, req, res, digest.Options{
			Method:   req.Method.String(),
			URI:      req.Recipient.Addr(),
			Username: auth.Username,
			Password: auth.Password,
		})
	}

	return digestTransactionRequest(ctx, c, req, res, digest.Options{
		Method:   req.Method.String(),
		URI:      req.Recipient.Addr(),
		Username: auth.Username,
		Password: auth.Password,
	})
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

	if v := req.Via(); v == nil {
		// Multi VIA value must be manually added
		ClientRequestAddVia(c, req)
	}

	// From and To headers should not contain Port numbers, headers, uri params
	if v := req.From(); v == nil {
		from := sip.FromHeader{
			DisplayName: c.UserAgent.name,
			Address: sip.Uri{
				User:      c.UserAgent.name,
				Host:      c.UserAgent.hostname,
				UriParams: sip.NewParams(),
				Headers:   sip.NewParams(),
			},
			Params: sip.NewParams(),
		}

		if from.Address.Host == "" {
			// In case we have no UA hostname set use whatever is our routing host
			from.Address.Host = c.host
		}

		from.Params.Add("tag", sip.GenerateTagN(16))
		req.AppendHeader(&from)
	}

	if v := req.To(); v == nil {
		to := sip.ToHeader{
			Address: sip.Uri{
				Encrypted: req.Recipient.Encrypted,
				User:      req.Recipient.User,
				Host:      req.Recipient.Host,
				UriParams: sip.NewParams(),
				Headers:   sip.NewParams(),
			},
			Params: sip.NewParams(),
		}
		req.AppendHeader(&to)
	}

	if v := req.CallID(); v == nil {
		uuid, err := uuid.NewRandom()
		if err != nil {
			return err
		}

		callid := sip.CallIDHeader(uuid.String())
		req.AppendHeader(&callid)

	}

	if v := req.CSeq(); v == nil {
		cseq := sip.CSeqHeader{
			SeqNo:      1,
			MethodName: req.Method,
		}
		req.AppendHeader(&cseq)
	}

	if v := req.MaxForwards(); v == nil {
		maxfwd := sip.MaxForwardsHeader(70)
		req.AppendHeader(&maxfwd)
	}

	if req.Body() == nil {
		req.SetBody(nil)
	}

	return nil
}

// ClientRequestBuild will build missing fields in request
// This is by default but can be used to combine with other ClientRequestOptions
func ClientRequestBuild(c *Client, r *sip.Request) error {
	return clientRequestBuildReq(c, r)
}

// ClientRequestAddVia is option for adding via header
// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261.html#section-16.6
func ClientRequestAddVia(c *Client, r *sip.Request) error {
	// TODO
	// A client that sends a request to a multicast address MUST add the
	// "maddr" parameter to its Via header field value containing the
	// destination multicast address

	newvia := &sip.ViaHeader{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       r.Transport(),
		Host:            c.host, // This can be rewritten by transport layer
		Port:            c.port, // This can be rewritten by transport layer
		Params:          sip.NewParams(),
	}
	// NOTE: Consider lenght of branch configurable
	newvia.Params.Add("branch", sip.GenerateBranchN(16))
	if c.rport {
		newvia.Params.Add("rport", "")
	}

	if via := r.Via(); via != nil {
		// https://datatracker.ietf.org/doc/html/rfc3581#section-6
		// As proxy rport and received must be fullfiled
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
	// We will try to use our listen port. Host must be set to some none NAT IP
	port := c.tp.GetListenPort(sip.NetworkToLower(r.Transport()))

	rr := &sip.RecordRouteHeader{
		Address: sip.Uri{
			Host: c.host,
			Port: port, // This must be listen port
			UriParams: sip.HeaderParams{
				// Transport must be provided as wesll
				// https://datatracker.ietf.org/doc/html/rfc5658
				"transport": sip.NetworkToLower(r.Transport()),
				"lr":        "",
			},
			Headers: sip.NewParams(),
		},
	}

	r.PrependHeader(rr)
	return nil
}

// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261#section-16
// ClientRequestDecreaseMaxForward should be used when forwarding request. It decreases max forward
// in case of 0 it returnes error
func ClientRequestDecreaseMaxForward(c *Client, r *sip.Request) error {
	maxfwd := r.MaxForwards()
	if maxfwd == nil {
		// TODO, should we return error here
		return nil
	}

	maxfwd.Dec()

	if maxfwd.Val() <= 0 {
		return fmt.Errorf("Max forwards reached")
	}
	return nil
}
