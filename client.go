package sipgo

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"

	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"github.com/icholy/digest"
)

func Init() {
	uuid.EnableRandPool()
}

type ClientTransactionRequester interface {
	Request(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error)
}

type Client struct {
	*UserAgent
	host  string
	port  int
	rport bool
	log   *slog.Logger

	connAddr sip.Addr

	// TxRequester allows you to use your transaction requester instead default from transaction layer
	// Useful only for testing
	//
	// Experimental
	TxRequester ClientTransactionRequester
}

type ClientOption func(c *Client) error

// WithClientLogger allows customizing client logger
func WithClientLogger(logger *slog.Logger) ClientOption {
	return func(s *Client) error {
		s.log = logger
		return nil
	}
}

// WithClientHost allows setting default route host or IP on Via
// NOTE: From header hostname is WithUserAgentHostname option on UA or modify request manually
func WithClientHostname(hostname string) ClientOption {
	return func(s *Client) error {
		s.host = hostname
		return nil
	}
}

// WithClientPort allows setting default route Via port
// TransportLayer.ConnectionReuse is set to false
// default: ephemeral port
func WithClientPort(port int) ClientOption {
	return func(s *Client) error {
		s.port = port
		return nil
	}
}

// WithClientConnectionAddr forces request to send connection with this local addr.
// This is useful when you need to act as client first and avoid creating server handle listeners.
func WithClientConnectionAddr(hostPort string) ClientOption {
	return func(s *Client) error {
		host, port, err := sip.ParseAddr(hostPort)
		if err != nil {
			return err
		}
		s.connAddr = sip.Addr{
			IP:       net.ParseIP(host),
			Port:     port,
			Hostname: host,
		}
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
		log:       sip.DefaultLogger().With("caller", "Client"),
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

// Hostname returns default hostname or what is set WithHostname option
func (c *Client) Hostname() string {
	return c.host
}

// TransactionRequest uses transaction layer to send request and returns transaction
// For more correct behavior use client.Do instead which acts same like HTTP req/response
//
// By default request will not be cloned and it will populate request with missing headers unless options are used
// In most cases you want this as you will retry with additional headers
//
// Following header fields will be added if not exist to have correct SIP request:
// To, From, CSeq, Call-ID, Max-Forwards, Via
// Passing options will override this behavior, that is, it is expected that your request is already prebuild
// This is mostly the case when creating proxy
func (c *Client) TransactionRequest(ctx context.Context, req *sip.Request, options ...ClientRequestOption) (sip.ClientTransaction, error) {
	if req.IsAck() {
		return nil, fmt.Errorf("ACK request must be sent directly through transport. Use WriteRequest")
	}

	if len(options) == 0 {
		clientRequestBuildReq(c, req)
	} else {
		for _, o := range options {
			if err := o(c, req); err != nil {
				return nil, err
			}
		}
	}

	if c.TxRequester != nil {
		return c.TxRequester.Request(ctx, req)
	}

	// Do some request validation, but only place as warning
	// The Content-Length header field value is used to locate the end of
	//   each SIP message in a stream.  It will always be present when SIP
	//   messages are sent over stream-oriented transports.
	if sip.IsReliable(req.Transport()) && req.ContentLength() == nil {
		c.log.Warn("Missing Content-Length for reliable transport")
	}

	return c.tx.Request(ctx, req)
}

func (c *Client) newTransaction(ctx context.Context, req *sip.Request, onConnection func(conn sip.Connection) error, options ...ClientRequestOption) (sip.ClientTransaction, error) {
	if len(options) == 0 {
		clientRequestBuildReq(c, req)
	} else {
		for _, o := range options {
			if err := o(c, req); err != nil {
				return nil, err
			}
		}
	}

	if c.TxRequester != nil {
		return c.TxRequester.Request(ctx, req)
	}

	tx, err := c.tx.NewClientTransaction(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := onConnection(tx.Connection()); err != nil {
		tx.Terminate()
		return nil, err
	}

	err = tx.Init()
	if err != nil {
		tx.Terminate()
	}
	return tx, err
}

// Do request is HTTP client like Do request/response.
// It returns on final response.
// NOTE: Canceling ctx WILL not send Cancel Request which is needed for INVITE. Use dialog API for dealing with dialogs
// For more lower API use TransactionRequest directly
func (c *Client) Do(ctx context.Context, req *sip.Request, opts ...ClientRequestOption) (*sip.Response, error) {
	tx, err := c.TransactionRequest(ctx, req, opts...)
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
			return nil, ctx.Err()
		}
	}
}

type DigestAuth struct {
	Username string
	Password string
}

// DoDigestAuth  will apply digest authentication if initial request is chalenged by 401 or 407.
func (c *Client) DoDigestAuth(ctx context.Context, req *sip.Request, res *sip.Response, auth DigestAuth) (*sip.Response, error) {
	tx, err := c.TransactionDigestAuth(ctx, req, res, auth)
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
			return nil, ctx.Err()
		}
	}
}

// TransactionDigestAuth will apply digest authentication if initial request is chalenged by 401 or 407.
// It returns new transaction that is created for this request
func (c *Client) TransactionDigestAuth(ctx context.Context, req *sip.Request, res *sip.Response, auth DigestAuth) (sip.ClientTransaction, error) {
	if res.StatusCode == sip.StatusProxyAuthRequired {
		return digestProxyAuthRequest(ctx, c, req, res, digest.Options{
			Method:   req.Method.String(),
			URI:      req.Recipient.Addr(),
			Username: auth.Username,
			Password: auth.Password,
		})
	}

	return c.digestTransactionRequest(ctx, req, res, digest.Options{
		Method:   req.Method.String(),
		URI:      req.Recipient.Addr(),
		Username: auth.Username,
		Password: auth.Password,
	})
}

// digestTransactionRequest does basic digest auth
func (c *Client) digestTransactionRequest(ctx context.Context, req *sip.Request, res *sip.Response, opts digest.Options) (sip.ClientTransaction, error) {
	if err := digestAuthApply(req, res, opts); err != nil {
		return nil, err
	}

	cseq := req.CSeq()
	cseq.SeqNo++

	req.RemoveHeader("Via")
	tx, err := c.TransactionRequest(ctx, req, ClientRequestAddVia)
	return tx, err
}

// WriteRequest sends request directly to transport layer
// Behavior is same as TransactionRequest
// Non-transaction ACK request should be passed like this
func (c *Client) WriteRequest(req *sip.Request, options ...ClientRequestOption) error {
	if len(options) == 0 {
		clientRequestBuildReq(c, req)
		return c.writeReq(req)
	}

	for _, o := range options {
		if err := o(c, req); err != nil {
			return err
		}
	}
	return c.writeReq(req)
}

func (c *Client) writeReq(req *sip.Request) error {
	if c.TxRequester != nil {
		_, err := c.TxRequester.Request(context.TODO(), req)
		return err
	}
	return c.tp.WriteMsg(req)
}

type ClientRequestOption func(c *Client, req *sip.Request) error

// ClientRequestBuild will build missing fields in request
// This is by default but can be used to combine with other ClientRequestOptions
func ClientRequestBuild(c *Client, r *sip.Request) error {
	return clientRequestBuildReq(c, r)
}

func clientRequestBuildReq(c *Client, req *sip.Request) error {
	// https://www.rfc-editor.org/rfc/rfc3261#section-8.1.1
	// A valid SIP request formulated by a UAC MUST, at a minimum, contain
	// the following header fields: To, From, CSeq, Call-ID, Max-Forwards,
	// and Via;

	mustHeader := make([]sip.Header, 0, 6)
	if v := req.Via(); v == nil {
		// Multi VIA value must be manually added
		via := clientRequestCreateVia(c, req)
		mustHeader = append(mustHeader, via)
	}

	// From and To headers should not contain Port numbers, headers, uri params
	if v := req.From(); v == nil {
		from := sip.FromHeader{
			DisplayName: c.UserAgent.name,
			Address: sip.Uri{
				Scheme:    req.Recipient.Scheme,
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
		mustHeader = append(mustHeader, &from)
	}

	if v := req.To(); v == nil {
		to := sip.ToHeader{
			Address: sip.Uri{
				Scheme:    req.Recipient.Scheme,
				User:      req.Recipient.User,
				Host:      req.Recipient.Host,
				UriParams: sip.NewParams(),
				Headers:   sip.NewParams(),
			},
			Params: sip.NewParams(),
		}
		mustHeader = append(mustHeader, &to)
	}

	if v := req.CallID(); v == nil {
		uuid, err := uuid.NewRandom()
		if err != nil {
			return err
		}

		callid := sip.CallIDHeader(uuid.String())
		mustHeader = append(mustHeader, &callid)

	}

	if v := req.CSeq(); v == nil {
		var b [4]byte
		_, err := rand.Read(b[:])
		if err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(b[:]) & 0x7FFFFFFF // ensure < 2^31
		n = max(1<<31-100, n)
		cseq := sip.CSeqHeader{
			SeqNo:      n,
			MethodName: req.Method,
		}
		mustHeader = append(mustHeader, &cseq)
	}

	if v := req.MaxForwards(); v == nil {
		maxfwd := sip.MaxForwardsHeader(70)
		mustHeader = append(mustHeader, &maxfwd)
	}

	req.PrependHeader(mustHeader...)

	if req.Body() == nil {
		req.SetBody(nil)
	}

	// Set local addr, transport layer will check is present
	if c.connAddr.IP != nil {
		// Doing a copy to avoid dangling ip
		c.connAddr.Copy(&req.Laddr)
	}

	return nil
}

// ClientRequestAddVia is option for adding via header
// Based on proxy setup https://www.rfc-editor.org/rfc/rfc3261.html#section-16.6
func ClientRequestAddVia(c *Client, r *sip.Request) error {
	via := clientRequestCreateVia(c, r)
	r.PrependHeader(via)
	return nil
}

// ClientRequestRegisterBuild builds correctly REGISTER request based on RFC
// Whenever you send REGISTER request you should pass this option
// https://datatracker.ietf.org/doc/html/rfc3261#section-10.2
//
// Experimental
func ClientRequestRegisterBuild(c *Client, r *sip.Request) error {
	// Register generally run in a loop
	if cseq := r.CSeq(); cseq != nil {
		// Increase cseq if this is existing transaction
		// WriteRequest for ex ACK will not increase and this is wanted behavior
		// This will be a problem if we allow ACK to be passed as transaction request
		cseq.SeqNo++
	}

	if err := clientRequestBuildReq(c, r); err != nil {
		return err
	}

	// address-of-record MUST
	// be a SIP URI or SIPS URI.
	// NOTE for now we expect client will build TO and From header correctly

	// The "userinfo" and "@" components of the
	//        SIP URI MUST NOT be present.
	r.Recipient.User = ""
	return nil
}

func clientRequestCreateVia(c *Client, r *sip.Request) *sip.ViaHeader {
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
	return newvia
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
		return nil
	}

	maxfwd.Dec()

	if maxfwd.Val() <= 0 {
		return fmt.Errorf("max forwards reached")
	}
	return nil
}

func ClientRequestIncreaseCSEQ(c *Client, req *sip.Request) error {
	if cseq := req.CSeq(); cseq != nil {
		// Increase cseq if this is new transaction but has cseq added.
		// Request within dialog should not have this behavior
		// WriteRequest for ex ACK will not increase and this is wanted behavior
		// This will be a problem if we allow ACK to be passed as transaction request
		cseq.SeqNo++
		cseq.MethodName = req.Method
	}
	return nil
}

func digestProxyAuthApply(req *sip.Request, res *sip.Response, opts digest.Options) error {
	authHeader := res.GetHeader("Proxy-Authenticate")
	if authHeader == nil {
		return fmt.Errorf("No Proxy-Authenticate header present")
	}
	chal, err := digest.ParseChallenge(authHeader.Value())
	if err != nil {
		return fmt.Errorf("fail to parse challenge authHeader=%q: %w", authHeader.Value(), err)
	}

	// Fix lower case algorithm although not supported by rfc
	chal.Algorithm = sip.ASCIIToUpper(chal.Algorithm)

	// Reply with digest
	cred, err := digest.Digest(chal, opts)
	if err != nil {
		return fmt.Errorf("fail to build digest: %w", err)
	}

	req.RemoveHeader("Proxy-Authorization")
	req.AppendHeader(sip.NewHeader("Proxy-Authorization", cred.String()))
	return nil
}

func digestAuthApply(req *sip.Request, res *sip.Response, opts digest.Options) error {
	wwwAuth := res.GetHeader("WWW-Authenticate")
	if wwwAuth == nil {
		return fmt.Errorf("No WWW-Authenticate header present")
	}

	chal, err := digest.ParseChallenge(wwwAuth.Value())
	if err != nil {
		return fmt.Errorf("fail to parse chalenge wwwauth=%q: %w", wwwAuth.Value(), err)
	}

	// Fix lower case algorithm although not supported by rfc
	chal.Algorithm = sip.ASCIIToUpper(chal.Algorithm)

	// Reply with digest
	cred, err := digest.Digest(chal, opts)
	if err != nil {
		return fmt.Errorf("fail to build digest: %w", err)
	}

	req.RemoveHeader("Authorization")
	req.AppendHeader(sip.NewHeader("Authorization", cred.String()))
	return nil
}

// digestProxyAuthRequest does basic digest auth with proxy header
func digestProxyAuthRequest(ctx context.Context, client *Client, req *sip.Request, res *sip.Response, opts digest.Options) (sip.ClientTransaction, error) {
	if err := digestProxyAuthApply(req, res, opts); err != nil {
		return nil, err
	}

	cseq := req.CSeq()
	cseq.SeqNo++

	req.RemoveHeader("Via")
	tx, err := client.TransactionRequest(ctx, req, ClientRequestAddVia)
	return tx, err
}
