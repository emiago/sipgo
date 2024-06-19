package sip

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Request RFC 3261 - 7.1.
type Request struct {
	MessageData
	Method    RequestMethod
	Recipient Uri
}

// NewRequest creates base for building sip Request
// A Request-Line contains a method name, a Request-URI, and the SIP/2.0 as version
// No headers are added. AppendHeader should be called to add Headers.
// r.SetBody can be called to set proper ContentLength header
func NewRequest(method RequestMethod, recipient Uri) *Request {
	if recipient.UriParams != nil {
		// mostly this are empty
		recipient.UriParams = recipient.UriParams.clone()
	}
	if recipient.Headers != nil {
		recipient.Headers = recipient.Headers.clone()
	}

	req := &Request{}
	req.SipVersion = "SIP/2.0"
	// req.headers = newHeaders()
	req.headers = headers{
		// headers:     make(map[string]Header),
		headerOrder: make([]Header, 0),
	}
	req.Method = method
	req.Recipient = recipient
	req.body = nil

	return req
}

func (req *Request) Short() string {
	if req == nil {
		return "<nil>"
	}

	return fmt.Sprintf("request method=%s Recipient=%s transport=%s source=%s",
		req.Method,
		req.Recipient.String(),
		req.Transport(),
		req.Source(),
	)
}

// StartLine returns Request Line - RFC 2361 7.1.
func (req *Request) StartLine() string {
	var buffer strings.Builder
	req.StartLineWrite(&buffer)
	return buffer.String()
}

func (req *Request) StartLineWrite(buffer io.StringWriter) {
	buffer.WriteString(string(req.Method))
	buffer.WriteString(" ")
	buffer.WriteString(req.Recipient.String())
	buffer.WriteString(" ")
	buffer.WriteString(req.SipVersion)
}

func (req *Request) String() string {
	var buffer strings.Builder
	req.StringWrite(&buffer)
	return buffer.String()
}

func (req *Request) StringWrite(buffer io.StringWriter) {
	// 	The start-line, each message-header line, and the empty line MUST be
	//  terminated by a carriage-return line-feed sequence (CRLF).  Note that
	//  the empty line MUST be present even if the message-body is not.
	req.StartLineWrite(buffer)
	buffer.WriteString("\r\n")
	// Write the headers.
	req.headers.StringWrite(buffer)
	// Empty line
	buffer.WriteString("\r\n")
	// message body
	if req.body != nil {
		buffer.WriteString(string(req.body))
		return
	}
	// buffer.WriteString("\r\n")
}

func (req *Request) Clone() *Request {
	return cloneRequest(req)
}

func (req *Request) IsInvite() bool {
	return req.Method == INVITE
}

func (req *Request) IsAck() bool {
	return req.Method == ACK
}

func (req *Request) IsCancel() bool {
	return req.Method == CANCEL
}

func (req *Request) Transport() string {
	if tp := req.MessageData.Transport(); tp != "" {
		return tp
	}

	var tp string
	if viaHop := req.Via(); viaHop != nil && viaHop.Transport != "" {
		tp = viaHop.Transport
	} else {
		tp = DefaultProtocol
	}

	uri := req.Recipient
	if hdr := req.Route(); hdr != nil {
		uri = hdr.Address
	}

	if uri.UriParams != nil {
		if val, ok := uri.UriParams.Get("transport"); ok && val != "" {
			tp = strings.ToUpper(val)
		}
	}

	if uri.IsEncrypted() {
		if tp == "TCP" {
			tp = "TLS"
		} else if tp == "WS" {
			tp = "WSS"
		}
	}

	//TODO string is expensive
	// if tp == "UDP" && len(req.String()) > int(MTU)-200 {
	// 	tp = "TCP"
	// }

	return tp
}

// Source will return host:port address
// In case of network parsed request source will be connection remote address
func (req *Request) Source() string {
	if src := req.MessageData.Source(); src != "" {
		return src
	}

	viaHop := req.Via()
	if viaHop == nil {
		return ""
	}

	var (
		host string
		port int
	)

	host = viaHop.Host
	if viaHop.Port > 0 {
		port = viaHop.Port
	} else {
		port = int(DefaultPort(req.Transport()))
	}

	// https://datatracker.ietf.org/doc/html/rfc3581#section-4
	if viaHop.Params != nil {
		if received, ok := viaHop.Params.Get("received"); ok && received != "" {
			host = received
		}
		if rport, ok := viaHop.Params.Get("rport"); ok && rport != "" {
			if p, err := strconv.Atoi(rport); err == nil {
				port = p
			}
		}
	}

	return fmt.Sprintf("%v:%v", host, port)
}

func (req *Request) Destination() string {
	if dest := req.MessageData.Destination(); dest != "" {
		return dest
	}

	var uri *Uri
	if hdr := req.Route(); hdr != nil {
		// TODO This may be a problem if we are not a proxy, as we also need to remove this header
		// https://datatracker.ietf.org/doc/html/rfc2543#section-6.29
		// any client MUST send any
		// subsequent requests for this call leg to the first Request-URI in the
		// Route request header field and remove that entry.
		uri = &hdr.Address
	}
	if uri == nil {
		uri = &req.Recipient
	}

	host := uri.Host
	if uri.Port > 0 {
		return fmt.Sprintf("%v:%v", host, uri.Port)
	}

	port := DefaultPort(req.Transport())
	return fmt.Sprintf("%v:%v", host, port)
}

// NewAckRequest creates ACK request for 2xx INVITE
// https://tools.ietf.org/html/rfc3261#section-13.2.2.4
// NOTE: it does not copy Via header. This is left to transport or caller to enforce
// Deprecated: use DialogClient for building dialogs
func NewAckRequest(inviteRequest *Request, inviteResponse *Response, body []byte) *Request {
	Recipient := &inviteRequest.Recipient
	if contact := inviteResponse.Contact(); contact != nil {
		Recipient = &contact.Address
	}
	ackRequest := NewRequest(
		ACK,
		*Recipient.Clone(),
	)
	ackRequest.SipVersion = inviteRequest.SipVersion

	if len(inviteRequest.GetHeaders("Route")) > 0 {
		CopyHeaders("Route", inviteRequest, ackRequest)
	} else {
		// https://datatracker.ietf.org/doc/html/rfc2543#section-6.29
		hdrs := inviteResponse.GetHeaders("Record-Route")
		for i := len(hdrs) - 1; i >= 0; i-- {
			recordRoute := hdrs[i]
			ackRequest.AppendHeader(NewHeader("Route", recordRoute.Value()))
		}
	}

	maxForwardsHeader := MaxForwardsHeader(70)
	ackRequest.AppendHeader(&maxForwardsHeader)
	if h := inviteRequest.From(); h != nil {
		ackRequest.AppendHeader(h.headerClone())
	}

	if h := inviteResponse.To(); h != nil {
		ackRequest.AppendHeader(h.headerClone())
	}

	if h := inviteRequest.CallID(); h != nil {
		ackRequest.AppendHeader(h.headerClone())
	}

	if h := inviteRequest.CSeq(); h != nil {
		ackRequest.AppendHeader(h.headerClone())
	}

	cseq := ackRequest.CSeq()
	cseq.MethodName = ACK

	/*
	   	A UAC SHOULD include a Contact header field in any target refresh
	    requests within a dialog, and unless there is a need to change it,
	    the URI SHOULD be the same as used in previous requests within the
	    dialog.  If the "secure" flag is true, that URI MUST be a SIPS URI.
	    As discussed in Section 12.2.2, a Contact header field in a target
	    refresh request updates the remote target URI.  This allows a UA to
	    provide a new contact address, should its address change during the
	    duration of the dialog.
	*/

	if h := inviteRequest.Contact(); h != nil {
		ackRequest.AppendHeader(h.headerClone())
	}

	ackRequest.SetBody(body)
	ackRequest.SetTransport(inviteRequest.Transport())
	ackRequest.SetSource(inviteRequest.Source())
	ackRequest.SetDestination(inviteRequest.Destination())

	return ackRequest
}

func newAckRequestNon2xx(inviteRequest *Request, inviteResponse *Response, body []byte) *Request {
	ackRequest := NewAckRequest(inviteRequest, inviteResponse, body)

	CopyHeaders("Via", inviteRequest, ackRequest)
	if inviteResponse.IsSuccess() {
		// update branch, 2xx ACK is separate Tx
		viaHop := ackRequest.Via()
		viaHop.Params.Add("branch", GenerateBranch())
	}
	return ackRequest
}

func newCancelRequest(requestForCancel *Request) *Request {
	cancelReq := NewRequest(
		CANCEL,
		requestForCancel.Recipient,
	)
	cancelReq.SipVersion = requestForCancel.SipVersion

	viaHop := requestForCancel.Via()
	cancelReq.AppendHeader(viaHop.Clone())
	CopyHeaders("Route", requestForCancel, cancelReq)
	maxForwardsHeader := MaxForwardsHeader(70)
	cancelReq.AppendHeader(&maxForwardsHeader)

	if h := requestForCancel.From(); h != nil {
		cancelReq.AppendHeader(h.headerClone())
	}
	if h := requestForCancel.To(); h != nil {
		cancelReq.AppendHeader(h.headerClone())
	}
	if h := requestForCancel.CallID(); h != nil {
		cancelReq.AppendHeader(h.headerClone())
	}
	if h := requestForCancel.CSeq(); h != nil {
		cancelReq.AppendHeader(h.headerClone())
	}
	cseq := cancelReq.CSeq()
	cseq.MethodName = CANCEL

	// cancelReq.SetBody([]byte{})
	cancelReq.SetTransport(requestForCancel.Transport())
	cancelReq.SetSource(requestForCancel.Source())
	cancelReq.SetDestination(requestForCancel.Destination())

	return cancelReq
}

func cloneRequest(req *Request) *Request {
	newReq := NewRequest(
		req.Method,
		*req.Recipient.Clone(),
	)
	newReq.SipVersion = req.SipVersion

	for _, h := range req.CloneHeaders() {
		newReq.AppendHeader(h)
	}
	// for _, h := range cloneHeaders(req) {
	// 	newReq.AppendHeader(h)
	// }

	// newReq.SetBody(req.Body())
	newReq.SetTransport(req.Transport())
	newReq.SetSource(req.Source())
	newReq.SetDestination(req.Destination())

	return newReq
}

func CopyRequest(req *Request) *Request {
	return cloneRequest(req)
}
