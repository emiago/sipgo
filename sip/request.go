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
	method    RequestMethod
	recipient *Uri
}

func NewRequest(
	method RequestMethod,
	recipient *Uri,
	sipVersion string,
) *Request {
	req := &Request{}
	// req.startLineWrite = req.StartLineWrite
	req.SipVersion = sipVersion
	// req.headers = newHeaders()
	req.headers = headers{
		// headers:     make(map[string]Header),
		headerOrder: make([]Header, 0),
	}
	req.method = method
	req.recipient = recipient
	req.body = nil

	return req
}

func (req *Request) Short() string {
	if req == nil {
		return "<nil>"
	}

	return fmt.Sprintf("request method=%s recipient=%s transport=%s source=%s",
		req.Method(),
		req.Recipient().String(),
		req.Transport(),
		req.Source(),
	)
}

func (req *Request) Method() RequestMethod {
	return req.method
}
func (req *Request) SetMethod(method RequestMethod) {
	req.method = method
}

func (req *Request) Recipient() *Uri {
	return req.recipient
}
func (req *Request) SetRecipient(recipient *Uri) {
	req.recipient = recipient
}

// StartLine returns Request Line - RFC 2361 7.1.
func (req *Request) StartLine() string {
	var buffer strings.Builder
	req.StartLineWrite(&buffer)
	return buffer.String()
}

func (req *Request) StartLineWrite(buffer io.StringWriter) {
	buffer.WriteString(string(req.Method()))
	buffer.WriteString(" ")
	buffer.WriteString(req.Recipient().String())
	buffer.WriteString(" ")
	buffer.WriteString(req.SipVersion)
}

func (req *Request) String() string {
	var buffer strings.Builder
	req.StringWrite(&buffer)
	return buffer.String()
}

func (req *Request) StringWrite(buffer io.StringWriter) {
	req.StartLineWrite(buffer)
	buffer.WriteString("\r\n")
	// Write the headers.
	req.headers.StringWrite(buffer)
	// message body
	if req.body != nil {
		buffer.WriteString("\r\n")
		buffer.WriteString(string(req.body))
	}
	buffer.WriteString("\r\n")
}

func (req *Request) Clone() *Request {
	return cloneRequest(req)
}

func (req *Request) IsInvite() bool {
	return req.Method() == INVITE
}

func (req *Request) IsAck() bool {
	return req.Method() == ACK
}

func (req *Request) IsCancel() bool {
	return req.Method() == CANCEL
}

func (req *Request) Transport() string {
	if tp := req.MessageData.Transport(); tp != "" {
		return tp
	}

	var tp string
	if viaHop, ok := req.Via(); ok && viaHop.Transport != "" {
		tp = viaHop.Transport
	} else {
		tp = DefaultProtocol
	}

	uri := req.Recipient()
	if hdr := req.GetHeader("Route"); hdr != nil {
		routeHeader := hdr.(*RouteHeader)
		uri = &routeHeader.Address
	}

	if uri != nil {
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
	}

	//TODO string is expensive
	// if tp == "UDP" && len(req.String()) > int(MTU)-200 {
	// 	tp = "TCP"
	// }

	return tp
}

func (req *Request) Source() string {
	if src := req.MessageData.Source(); src != "" {
		return src
	}

	viaHop, ok := req.Via()
	if !ok {
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
	if hdr := req.GetHeader("Route"); hdr != nil {
		routeHeader := hdr.(*RouteHeader)
		uri = &routeHeader.Address

	}
	if uri == nil {
		if u := req.Recipient(); u != nil {
			uri = u
		} else {
			return ""
		}
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
func NewAckRequest(inviteRequest *Request, inviteResponse *Response, body []byte) *Request {
	recipient := inviteRequest.Recipient()
	if contact, ok := inviteResponse.Contact(); ok {
		// For ws and wss (like clients in browser), don't use Contact
		if strings.Index(strings.ToLower(recipient.String()), "transport=ws") == -1 {
			recipient = &contact.Address
		}
	}
	ackRequest := NewRequest(
		ACK,
		recipient,
		inviteRequest.SipVersion,
	)
	CopyHeaders("Via", inviteRequest, ackRequest)
	if inviteResponse.IsSuccess() {
		// update branch, 2xx ACK is separate Tx
		viaHop, _ := ackRequest.Via()
		viaHop.Params.Add("branch", GenerateBranch())
	}

	if len(inviteRequest.GetHeaders("Route")) > 0 {
		CopyHeaders("Route", inviteRequest, ackRequest)
	} else {
		hdrs := inviteResponse.GetHeaders("Record-Route")
		for i := len(hdrs) - 1; i >= 0; i-- {
			r := &RouteHeader{}
			// NOT TESTED COULD BREAK
			h := hdrs[i].(*RecordRouteHeader).Clone()
			rrs := make([]*RecordRouteHeader, 0)

			for hop := h; hop != nil; hop = hop.Next {
				rrs = append(rrs, hop)
			}

			hop := h
			for j := len(rrs) - 1; j >= 0; j-- {
				hop = rrs[j]
				hop = hop.Next
			}
			ackRequest.AppendHeader(r)
		}
	}

	maxForwardsHeader := MaxForwards(70)
	ackRequest.AppendHeader(&maxForwardsHeader)
	CopyHeaders("From", inviteRequest, ackRequest)
	CopyHeaders("To", inviteResponse, ackRequest)
	CopyHeaders("Call-ID", inviteRequest, ackRequest)
	CopyHeaders("CSeq", inviteRequest, ackRequest)
	cseq, _ := ackRequest.CSeq()
	cseq.MethodName = ACK

	ackRequest.SetBody(body)
	ackRequest.SetTransport(inviteRequest.Transport())
	ackRequest.SetSource(inviteRequest.Source())
	ackRequest.SetDestination(inviteRequest.Destination())

	return ackRequest
}

func NewCancelRequest(requestForCancel *Request) *Request {
	cancelReq := NewRequest(
		CANCEL,
		requestForCancel.Recipient(),
		requestForCancel.SipVersion,
	)

	viaHop, _ := requestForCancel.Via()
	cancelReq.AppendHeader(viaHop.Clone())
	CopyHeaders("Route", requestForCancel, cancelReq)
	maxForwardsHeader := MaxForwards(70)
	cancelReq.AppendHeader(&maxForwardsHeader)
	CopyHeaders("From", requestForCancel, cancelReq)
	CopyHeaders("To", requestForCancel, cancelReq)
	CopyHeaders("Call-ID", requestForCancel, cancelReq)
	CopyHeaders("CSeq", requestForCancel, cancelReq)
	cseq, _ := cancelReq.CSeq()
	cseq.MethodName = CANCEL

	// cancelReq.SetBody([]byte{})
	cancelReq.SetTransport(requestForCancel.Transport())
	cancelReq.SetSource(requestForCancel.Source())
	cancelReq.SetDestination(requestForCancel.Destination())

	return cancelReq
}

func cloneRequest(req *Request) *Request {
	newReq := NewRequest(
		req.Method(),
		req.Recipient().Clone(),
		req.SipVersion,
	)

	for _, h := range req.CloneHeaders() {
		newReq.AppendHeader(h)
	}
	// for _, h := range cloneHeaders(req) {
	// 	newReq.AppendHeader(h)
	// }

	newReq.SetBody(req.Body())
	newReq.SetTransport(req.Transport())
	newReq.SetSource(req.Source())
	newReq.SetDestination(req.Destination())

	return newReq
}

func CopyRequest(req *Request) *Request {
	return cloneRequest(req)
}
