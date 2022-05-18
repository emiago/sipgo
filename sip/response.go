package sip

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	uuid "github.com/satori/go.uuid"
)

// Response RFC 3261 - 7.2.
// type Response interface {
// 	Message
// 	StatusCode() StatusCode
// 	SetStatusCode(code StatusCode)
// 	Reason() string
// 	SetReason(reason string)
// 	// Previous returns previous provisional responses
// 	Previous() []Response
// 	SetPrevious(responses []Response)
// 	/* Common helpers */
// 	IsProvisional() bool
// 	IsSuccess() bool
// 	IsRedirection() bool
// 	IsClientError() bool
// 	IsServerError() bool
// 	IsGlobalError() bool
// }

type Response struct {
	MessageData
	status   StatusCode
	reason   string
	previous []Response
}

func NewResponse(
	sipVersion string,
	statusCode StatusCode,
	reason string,
) *Response {
	res := &Response{}
	res.SipVersion = sipVersion
	res.headers = headers{
		// headers:     make(map[string]Header),
		headerOrder: make([]Header, 0),
	}
	res.status = statusCode
	res.reason = reason
	res.body = nil
	res.previous = make([]Response, 0)

	return res
}

func (res *Response) Short() string {
	if res == nil {
		return "<nil>"
	}

	return fmt.Sprintf("response status=%d reason=%s transport=%s source=%s",
		res.StatusCode(),
		res.Reason(),
		res.Transport(),
		res.Source(),
	)
}

func (res *Response) StatusCode() StatusCode {
	return res.status
}
func (res *Response) SetStatusCode(code StatusCode) {
	res.status = code
}

func (res *Response) Reason() string {
	return res.reason
}
func (res *Response) SetReason(reason string) {
	res.reason = reason
}

func (res *Response) Previous() []Response {
	return res.previous
}

func (res *Response) SetPrevious(responses []Response) {
	res.previous = responses
}

// StartLine returns Response Status Line - RFC 2361 7.2.
func (res *Response) StartLine() string {
	var buffer strings.Builder
	// Every SIP response starts with a Status Line - RFC 2361 7.2.
	res.StartLineWrite(&buffer)
	return buffer.String()
}

func (res *Response) StartLineWrite(buffer io.StringWriter) {
	statusCode := strconv.Itoa(int(res.StatusCode()))
	buffer.WriteString(res.SipVersion)
	buffer.WriteString(" ")
	buffer.WriteString(statusCode)
	buffer.WriteString(" ")
	buffer.WriteString(res.Reason())
}

func (res *Response) String() string {
	var buffer strings.Builder
	res.StringWrite(&buffer)
	return buffer.String()
}

func (res *Response) StringWrite(buffer io.StringWriter) {
	res.StartLineWrite(buffer)
	buffer.WriteString("\r\n")
	// Write the headers.
	res.headers.StringWrite(buffer)
	// message body
	if res.body != nil {
		buffer.WriteString("\r\n")
		buffer.WriteString(string(res.body))
	}
	buffer.WriteString("\r\n")
}

func (res *Response) Clone() *Response {
	return cloneResponse(res)
}

func (res *Response) IsProvisional() bool {
	return res.StatusCode() < 200
}

func (res *Response) IsSuccess() bool {
	return res.StatusCode() >= 200 && res.StatusCode() < 300
}

func (res *Response) IsRedirection() bool {
	return res.StatusCode() >= 300 && res.StatusCode() < 400
}

func (res *Response) IsClientError() bool {
	return res.StatusCode() >= 400 && res.StatusCode() < 500
}

func (res *Response) IsServerError() bool {
	return res.StatusCode() >= 500 && res.StatusCode() < 600
}

func (res *Response) IsGlobalError() bool {
	return res.StatusCode() >= 600
}

func (res *Response) IsAck() bool {
	if cseq, ok := res.CSeq(); ok {
		return cseq.MethodName == ACK
	}
	return false
}

func (res *Response) IsCancel() bool {
	if cseq, ok := res.CSeq(); ok {
		return cseq.MethodName == CANCEL
	}
	return false
}

func (res *Response) Transport() string {
	if tp := res.MessageData.Transport(); tp != "" {
		return tp
	}

	var tp string
	if viaHop, ok := res.Via(); ok && viaHop.Transport != "" {
		tp = viaHop.Transport
	} else {
		tp = DefaultProtocol
	}

	return tp
}

func (res *Response) Destination() string {
	if dest := res.MessageData.Destination(); dest != "" {
		return dest
	}

	viaHop, ok := res.Via()
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
		port = int(DefaultPort(res.Transport()))
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

// RFC 3261 - 8.2.6
func NewResponseFromRequest(
	req *Request,
	statusCode StatusCode,
	reason string,
	body []byte,
) *Response {
	res := NewResponse(
		req.SipVersion,
		statusCode,
		reason,
	)
	CopyHeaders("Record-Route", req, res)
	CopyHeaders("Via", req, res)
	CopyHeaders("From", req, res)
	CopyHeaders("To", req, res)
	CopyHeaders("Call-ID", req, res)
	CopyHeaders("CSeq", req, res)

	if statusCode == 100 {
		CopyHeaders("Timestamp", req, res)
	}

	if statusCode == 200 {
		if _, ok := res.from.Params["tag"]; !ok {
			uuid, _ := uuid.NewV4()
			res.from.Params["tag"] = uuid.String()
		}
	}

	if body != nil {
		res.SetBody(body)
	}

	res.SetTransport(req.Transport())
	res.SetSource(req.Destination())
	res.SetDestination(req.Source())

	return res
}

func cloneResponse(res *Response) *Response {
	newRes := NewResponse(
		res.SipVersion,
		res.StatusCode(),
		res.Reason(),
	)

	for _, h := range res.CloneHeaders() {
		newRes.AppendHeader(h)
	}

	newRes.SetBody(res.Body())

	newRes.SetPrevious(res.Previous())
	newRes.SetTransport(res.Transport())
	newRes.SetSource(res.Source())
	newRes.SetDestination(res.Destination())

	return newRes
}

func CopyResponse(res *Response) *Response {
	return cloneResponse(res)
}
